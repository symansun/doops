package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const (
	// cosBackupMultipartThreshold switches from single PutObject to S3 multipart
	// upload so large compressed audit/database archives stream with constant RAM.
	cosBackupMultipartThreshold = int64(8 << 20)
	// cosBackupPartSize is 8 MiB (above S3/COS's 5 MiB minimum part size).
	cosBackupPartSize = int64(8 << 20)
)

type cosBackupObject struct {
	Key          string
	Size         int64
	LastModified time.Time
}

type cosBackupStore interface {
	BucketName() string
	UploadFile(ctx context.Context, key, contentType, localPath string) (int64, error)
	ListPrefix(ctx context.Context, prefix string) ([]cosBackupObject, error)
	DeleteObject(ctx context.Context, key string) error
}

type s3COSBackupStore struct {
	client *s3.Client
	bucket string
}

// openCOSBackupStoreFunc is replaced in unit tests to verify upload, HeadObject
// verification, fail-closed purge, and prefix retention without hitting live COS.
var openCOSBackupStoreFunc = openCOSBackupStore

func openCOSBackupStore(ctx context.Context) (cosBackupStore, error) {
	secret, err := loadGatewayCOSConfig()
	if err != nil {
		return nil, err
	}
	client, err := newCOSClient(ctx, cosClientParams{
		Endpoint:        secret.Endpoint,
		Region:          secret.Region,
		AccessKeyID:     secret.AccessKeyID,
		SecretAccessKey: secret.SecretAccessKey,
	})
	if err != nil {
		return nil, err
	}
	return &s3COSBackupStore{client: client, bucket: secret.Bucket}, nil
}

func (b *s3COSBackupStore) BucketName() string { return b.bucket }

func (b *s3COSBackupStore) UploadFile(ctx context.Context, key, contentType, localPath string) (int64, error) {
	info, err := os.Stat(localPath)
	if err != nil {
		return 0, fmt.Errorf("backup_upload_stat_failed: %w", err)
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("backup_upload_invalid_file: %s is not a regular file", localPath)
	}
	size := info.Size()
	if size <= cosBackupMultipartThreshold {
		if err := b.uploadSingle(ctx, key, contentType, localPath, size); err != nil {
			return 0, err
		}
	} else {
		if err := b.uploadMultipart(ctx, key, contentType, localPath, size); err != nil {
			return 0, err
		}
	}
	out, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return 0, fmt.Errorf("backup_upload_verify_failed: key=%s: %w", key, err)
	}
	if out.ContentLength == nil || *out.ContentLength != size {
		got := int64(-1)
		if out.ContentLength != nil {
			got = *out.ContentLength
		}
		return 0, fmt.Errorf("backup_upload_size_mismatch: key=%s want=%d got=%d", key, size, got)
	}
	return size, nil
}

func (b *s3COSBackupStore) uploadSingle(ctx context.Context, key, contentType, localPath string, size int64) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("backup_upload_open_failed: %w", err)
	}
	defer f.Close()
	_, err = b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(b.bucket),
		Key:           aws.String(key),
		Body:          f,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("backup_upload_put_failed: key=%s: %w", key, err)
	}
	return nil
}

func (b *s3COSBackupStore) uploadMultipart(ctx context.Context, key, contentType, localPath string, size int64) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("backup_upload_open_failed: %w", err)
	}
	defer f.Close()

	created, err := b.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(b.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("backup_multipart_init_failed: key=%s: %w", key, err)
	}
	uploadID := aws.ToString(created.UploadId)
	abort := func() {
		abortCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = b.client.AbortMultipartUpload(abortCtx, &s3.AbortMultipartUploadInput{
			Bucket:   aws.String(b.bucket),
			Key:      aws.String(key),
			UploadId: aws.String(uploadID),
		})
	}

	buf := make([]byte, cosBackupPartSize)
	var parts []types.CompletedPart
	var partNum int32 = 1
	var uploaded int64
	for {
		n, readErr := io.ReadFull(f, buf)
		if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			abort()
			return fmt.Errorf("backup_multipart_read_failed: key=%s: %w", key, readErr)
		}
		if n == 0 {
			break
		}
		partOut, err := b.client.UploadPart(ctx, &s3.UploadPartInput{
			Bucket:        aws.String(b.bucket),
			Key:           aws.String(key),
			UploadId:      aws.String(uploadID),
			PartNumber:    aws.Int32(partNum),
			Body:          bytes.NewReader(buf[:n]),
			ContentLength: aws.Int64(int64(n)),
		})
		if err != nil {
			abort()
			return fmt.Errorf("backup_multipart_part_failed: key=%s part=%d: %w", key, partNum, err)
		}
		parts = append(parts, types.CompletedPart{
			ETag:       partOut.ETag,
			PartNumber: aws.Int32(partNum),
		})
		uploaded += int64(n)
		partNum++
		if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
			break
		}
	}
	if uploaded != size {
		abort()
		return fmt.Errorf("backup_multipart_size_changed: key=%s expected=%d uploaded=%d", key, size, uploaded)
	}
	_, err = b.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(b.bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: parts,
		},
	})
	if err != nil {
		abort()
		return fmt.Errorf("backup_multipart_complete_failed: key=%s: %w", key, err)
	}
	return nil
}

func (b *s3COSBackupStore) ListPrefix(ctx context.Context, prefix string) ([]cosBackupObject, error) {
	var out []cosBackupObject
	var token *string
	for {
		resp, err := b.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(b.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("backup_list_failed: prefix=%s: %w", prefix, err)
		}
		for _, item := range resp.Contents {
			obj := cosBackupObject{
				Key:  aws.ToString(item.Key),
				Size: aws.ToInt64(item.Size),
			}
			if item.LastModified != nil {
				obj.LastModified = item.LastModified.UTC()
			}
			out = append(out, obj)
		}
		if !aws.ToBool(resp.IsTruncated) || resp.NextContinuationToken == nil {
			break
		}
		token = resp.NextContinuationToken
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (b *s3COSBackupStore) DeleteObject(ctx context.Context, key string) error {
	_, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("backup_delete_failed: key=%s: %w", key, err)
	}
	return nil
}

// cleanCOSBackupPrefix validates an operator-supplied COS key prefix used for
// Gateway audit archives and store backups.
func cleanCOSBackupPrefix(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if strings.HasPrefix(trimmed, "/") || strings.Contains(trimmed, "\\") || strings.Contains(trimmed, "\x00") {
		return "", errors.New("backup_cos_prefix_invalid: cos prefix must be a relative key path without leading slash or backslash")
	}
	trimmed = strings.TrimRight(trimmed, "/")
	if trimmed == "" {
		return "", errors.New("backup_cos_prefix_invalid: cos prefix must not be empty")
	}
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '/':
		default:
			return "", fmt.Errorf("backup_cos_prefix_invalid: unsupported character %q in cos prefix", r)
		}
	}
	for _, seg := range strings.Split(trimmed, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", errors.New("backup_cos_prefix_invalid: cos prefix must not contain empty or traversal segments")
		}
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") || strings.Contains(cleaned, "/../") {
		return "", errors.New("backup_cos_prefix_invalid: cos prefix must not traverse parent directories")
	}
	return cleaned, nil
}

// pruneCOSBackupObjects removes objects strictly older than cutoff under
// prefix+"/", skipping any key in keepKeys so the just-uploaded archive is
// never deleted even if system clocks skew.
func pruneCOSBackupObjects(ctx context.Context, store cosBackupStore, prefix string, cutoff time.Time, keepKeys map[string]bool) (int, error) {
	objs, err := store.ListPrefix(ctx, prefix+"/")
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, obj := range objs {
		if keepKeys[obj.Key] || obj.LastModified.IsZero() || !obj.LastModified.Before(cutoff) {
			continue
		}
		if err := store.DeleteObject(ctx, obj.Key); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}
