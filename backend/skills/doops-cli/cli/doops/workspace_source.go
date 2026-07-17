package main

type WorkspaceSourceIdentity struct {
	Repository string `json:"repository"`
	Revision   string `json:"revision"`
	Branch     string `json:"branch,omitempty"`
	TreeDigest string `json:"treeDigest,omitempty"`
}

const workspaceManifestAPIVersion = "doops.sh/v3"
