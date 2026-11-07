package server

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// A target with no Helm credential still downloads a public Chart anonymously.
// When that anonymous request is refused, the operator needs to read the reason
// off the error: the repository wants a credential. A status code alone does
// not say that, and the same code from an authenticated request means something
// else entirely, so only the anonymous attempt is annotated.
func TestAnnotateAnonymousHelmChartRejectionNamesTheMissingCredential(t *testing.T) {
	for _, tc := range []struct {
		name      string
		in        error
		anonymous bool
		want      string
	}{
		{"anonymous 401", errors.New("helm_artifact_http_401"), true, "helm_chart_requires_credential"},
		{"anonymous 403", errors.New("helm_artifact_http_403"), true, "helm_chart_requires_credential"},
		// An authenticated 401 means the bound credential itself is wrong;
		// telling the operator to bind one would send them the wrong way.
		{"authenticated 401", errors.New("helm_artifact_http_401"), false, "helm_artifact_http_401"},
		{"authenticated 403", errors.New("helm_artifact_http_403"), false, "helm_artifact_http_403"},
		// Anything that is not an authentication refusal passes through: a
		// missing Chart or an unreachable repository is not a credential gap.
		{"anonymous 404", errors.New("helm_artifact_http_404"), true, "helm_artifact_http_404"},
		{"anonymous 500", errors.New("helm_artifact_http_500"), true, "helm_artifact_http_500"},
		{"unreachable", errors.New("helm_repository_unavailable"), true, "helm_repository_unavailable"},
		{"invalid chart", errors.New("helm_chart_invalid"), true, "helm_chart_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := annotateAnonymousHelmChartRejection(tc.in, tc.anonymous)
			if !strings.Contains(got.Error(), tc.want) {
				t.Fatalf("error = %q, want it to contain %q", got.Error(), tc.want)
			}
			// The original status code stays readable for diagnosis, and
			// errors.Is still matches what the fetch returned.
			if !errors.Is(got, tc.in) {
				t.Fatalf("annotation dropped the original error: %v", got)
			}
			if !strings.Contains(got.Error(), tc.in.Error()) {
				t.Fatalf("error = %q, want it to keep %q", got.Error(), tc.in.Error())
			}
		})
	}
	if annotateAnonymousHelmChartRejection(nil, true) != nil {
		t.Fatal("nil must stay nil")
	}
}

// The annotation is a prefix on a wrapped error, so a caller matching on the
// stable category still sees it, and the category stays lowercase/underscore.
func TestAnonymousHelmChartCategoryIsStable(t *testing.T) {
	err := annotateAnonymousHelmChartRejection(fmt.Errorf("helm_artifact_http_401"), true)
	category := strings.SplitN(err.Error(), ":", 2)[0]
	if category != "helm_chart_requires_credential" {
		t.Fatalf("category = %q, want helm_chart_requires_credential", category)
	}
	if !validCredentialErrorCategory(category) {
		t.Fatalf("category %q is not a stable error category", category)
	}
}
