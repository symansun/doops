package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type catalogRow struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Name        string            `json:"name"`
	Environment string            `json:"environment"`
	Cluster     string            `json:"cluster"`
	Instance    string            `json:"instance"`
	Details     map[string]string `json:"details"`
	Revision    int64             `json:"revision"`
}

var catalogFields = map[string][]string{"database": {"engine", "host", "port", "database"}, "website": {"scheme", "domain", "path"}, "image": {"registry", "repository", "version"}}
var hostPart = regexp.MustCompile("^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$")
var repoPart = regexp.MustCompile("^[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*$")
var imageVersion = regexp.MustCompile("^([A-Za-z0-9_][A-Za-z0-9_.-]{0,127}|sha256:[a-f0-9]{64})$")

func catalogHost(s string) bool {
	if net.ParseIP(s) != nil {
		return true
	}
	if s == "" || len(s) > 253 {
		return false
	}
	for _, v := range strings.Split(s, ".") {
		if !hostPart.MatchString(v) {
			return false
		}
	}
	return true
}
func catalogPort(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n > 0 && n <= 65535 && strconv.Itoa(n) == s
}
func catalogIdentity(r catalogRow) (string, bool) {
	fields := catalogFields[r.Kind]
	d := r.Details
	if len(fields) == 0 || len(d) != len(fields) {
		return "", false
	}
	parts := []string{}
	for _, f := range fields {
		v := d[f]
		if v == "" || len(v) > 253 || !inventoryMetadata(v) || strings.TrimSpace(v) != v {
			return "", false
		}
		parts = append(parts, v)
	}
	switch r.Kind {
	case "database":
		if !(d["engine"] == "PostgreSQL" || d["engine"] == "MySQL" || d["engine"] == "MongoDB") || !catalogHost(d["host"]) || !catalogPort(d["port"]) || !assetLabel.MatchString(d["database"]) {
			return "", false
		}
		parts[1] = strings.ToLower(parts[1])
	case "website":
		u, err := url.ParseRequestURI(d["path"])
		if (d["scheme"] != "https" && d["scheme"] != "http") || !catalogHost(d["domain"]) || err != nil || !strings.HasPrefix(d["path"], "/") || strings.HasPrefix(d["path"], "//") || u.RawQuery != "" || strings.ContainsAny(d["path"], "?#@\\%") {
			return "", false
		}
		parts[1] = strings.ToLower(parts[1])
	case "image":
		host := d["registry"]
		if strings.Contains(host, ":") {
			h, p, err := net.SplitHostPort(host)
			if err != nil || !catalogPort(p) {
				return "", false
			}
			host = h
		}
		if !catalogHost(host) || !repoPart.MatchString(d["repository"]) || !imageVersion.MatchString(d["version"]) {
			return "", false
		}
		parts[0] = strings.ToLower(parts[0])
	}
	b, _ := json.Marshal(parts)
	return string(b), true
}
func catalogText(v string, optional bool) bool {
	return optional && v == "" || assetLabel.MatchString(v) && inventoryMetadata(v)
}
func assetIssue(status int, code string) error { return machineInventoryProblem{status, code, code} }
func catalogHTTP(w http.ResponseWriter, result any, err error) {
	if err == nil {
		writeJSONHTTP(w, result)
		return
	}
	var p machineInventoryProblem
	if !errors.As(err, &p) {
		p = machineInventoryProblem{503, "asset_catalog_unavailable", "asset catalog unavailable"}
	}
	writeDoopsError(w, p.status, "", doopsErrorResponse{Error: p.code, Code: p.code, Message: p.message})
}
func catalogDecode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	return d.Decode(v) == nil && d.Decode(new(any)) == io.EOF
}
func (s *GatewayStore) catalogChange(ctx context.Context, a TokenAuth, action string, change func(*sql.Tx) error) error {
	id, err := s.StartAudit(AuditEvent{UserID: a.UserID, TokenID: a.TokenID, Action: GatewayAction(action), Session: "asset-catalog"})
	if err != nil {
		return assetIssue(503, "audit_unavailable")
	}
	err = func() error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err = change(tx); err != nil {
			return err
		}
		if err = finishAuditWith(tx, id, AuditFinish{Status: "success"}); err != nil {
			return assetIssue(503, "audit_unavailable")
		}
		return tx.Commit()
	}()
	if err != nil {
		if e := s.FinishAudit(id, AuditFinish{Status: "error", Error: "catalog_change_rejected"}); e != nil {
			return assetIssue(503, "audit_unavailable")
		}
	}
	return err
}

const catalogVisible = "((r.cluster='' AND r.created_by=?) OR (r.cluster<>'' AND EXISTS(SELECT 1 FROM permitted p WHERE p.cluster IN ('*',r.cluster) AND p.instance IN ('*',r.instance))))"

func (h *GatewayHub) HandleResourceCatalog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	a, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	ctx := r.Context()
	switch r.Method {
	case "GET":
		kind := r.URL.Query().Get("kind")
		if len(catalogFields[kind]) == 0 {
			catalogHTTP(w, nil, assetIssue(400, "invalid_kind"))
			return
		}
		rows, err := h.store.db.QueryContext(ctx, inventoryGrants+"SELECT r.id,r.kind,r.name,r.environment,r.cluster,r.instance,r.details,r.revision FROM inventory_resource_catalog r WHERE r.kind=? AND "+catalogVisible+" ORDER BY r.name,r.id", a.UserID, a.TokenID, kind, a.UserID)
		if err != nil {
			catalogHTTP(w, nil, err)
			return
		}
		defer rows.Close()
		out := []catalogRow{}
		for rows.Next() {
			var item catalogRow
			var details []byte
			if err = rows.Scan(&item.ID, &item.Kind, &item.Name, &item.Environment, &item.Cluster, &item.Instance, &details, &item.Revision); err != nil {
				break
			}
			if err = json.Unmarshal(details, &item.Details); err != nil {
				break
			}
			out = append(out, item)
		}
		if err == nil {
			err = rows.Err()
		}
		catalogHTTP(w, map[string]any{"records": out}, err)
	case "POST":
		var req struct {
			catalogRow
			Op string `json:"op"`
		}
		if !catalogDecode(w, r, &req) || len(catalogFields[req.Kind]) == 0 || !validCatalogOp(req.Op, req.ID, req.Revision) {
			catalogHTTP(w, nil, assetIssue(400, "invalid_record"))
			return
		}
		identity, valid := catalogIdentity(req.catalogRow)
		if req.Op != "delete" && (!valid || !catalogText(req.Name, false) || !catalogText(req.Environment, true) || ((req.Cluster != "" || req.Instance != "") && (!monitorID(req.Cluster) || !monitorID(req.Instance)))) {
			catalogHTTP(w, nil, assetIssue(400, "invalid_asset_details"))
			return
		}
		if req.Op == "create" {
			req.ID = randomHex(16)
		}
		err = h.store.catalogChange(ctx, a, "asset:catalog:"+req.Op, func(tx *sql.Tx) error {
			if req.Op != "create" {
				var c, i string
				e := tx.QueryRowContext(ctx, inventoryGrants+"SELECT r.cluster,r.instance FROM inventory_resource_catalog r WHERE r.id=? AND r.kind=? AND r.revision=? AND "+catalogVisible, a.UserID, a.TokenID, req.ID, req.Kind, req.Revision, a.UserID).Scan(&c, &i)
				if e == sql.ErrNoRows {
					return assetIssue(409, "record_conflict")
				}
				if e != nil {
					return e
				}
				if req.Op == "delete" {
					_, e = tx.Exec("DELETE FROM inventory_resource_catalog WHERE id=?", req.ID)
					return e
				}
				if c != "" && (c != req.Cluster || i != req.Instance) {
					return assetIssue(409, "asset_target_immutable")
				}
			}
			if req.Cluster != "" {
				var ok bool
				if e := tx.QueryRowContext(ctx, inventoryGrants+"SELECT EXISTS(SELECT 1 FROM agent_status s WHERE s.cluster=? AND s.instance=? AND EXISTS(SELECT 1 FROM permitted p WHERE p.cluster IN ('*',s.cluster) AND p.instance IN ('*',s.instance)))", a.UserID, a.TokenID, req.Cluster, req.Instance).Scan(&ok); e != nil {
					return e
				}
				if !ok {
					return assetIssue(403, "inventory_forbidden")
				}
			}
			b, _ := json.Marshal(req.Details)
			var res sql.Result
			var e error
			if req.Op == "create" {
				res, e = tx.Exec(gatewayUpsertSQL(gatewayUpsert{insert: "INSERT INTO inventory_resource_catalog(id,kind,cluster,instance,created_by,name,environment,details,identity_key,revision) VALUES(?,?,?,?,?,?,?,?,?,1)", doNothing: true}), req.ID, req.Kind, req.Cluster, req.Instance, a.UserID, req.Name, req.Environment, string(b), identity)
			} else {
				res, e = tx.Exec("UPDATE OR IGNORE inventory_resource_catalog SET cluster=?,instance=?,name=?,environment=?,details=?,identity_key=?,revision=revision+1 WHERE id=?", req.Cluster, req.Instance, req.Name, req.Environment, string(b), identity, req.ID)
			}
			if e != nil {
				return e
			}
			n, e := res.RowsAffected()
			if e == nil && n != 1 {
				return assetIssue(409, "asset_identity_exists")
			}
			return e
		})
		catalogHTTP(w, map[string]string{"id": req.ID}, err)
	default:
		catalogHTTP(w, nil, assetIssue(405, "method_not_allowed"))
	}
}
func validCatalogOp(op, id string, rev int64) bool {
	return op == "create" && id == "" && rev == 0 || (op == "update" || op == "delete") && monitorID(id) && rev > 0
}
