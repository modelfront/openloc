// Package extsdk is the contract between the server and an extension: a zip
// holding a manifest.json and a WASM module. The server uploads, validates
// and runs extensions; the WASM code talks to a vendor API through the host's
// http_request function, which only reaches the manifest's allowed hosts and
// fills in secrets, so an extension never holds the API token.
//
// Types in this file are shared by the server and extension code. The
// guest-side helpers (guest.go) build only for GOOS=wasip1.
package extsdk

// APIVersion is the contract version this server speaks. A manifest declaring
// a different api_version is rejected at upload.
const APIVersion = 1

// Capability names. An extension exports one function per capability it
// lists in its manifest.
const (
	CapabilityQE  = "qe"  // scores translations: approve or reject
	CapabilityAPE = "ape" // may also return a post-edited translation
)

// Exported function names.
const (
	FuncCheckKey = "check_key"
	FuncPredict  = "predict"
)

// HostFunction is the import name of the host's HTTP function, in Extism's
// user namespace ("extism:host/user").
const HostFunction = "http_request"

// Setting keys a v1 manifest may declare. The token is stored encrypted as
// the extension's API key; base_url is the endpoint of the key's connection.
const (
	SettingToken   = "token"
	SettingBaseURL = "base_url"
)

// SecretPlaceholder is replaced by the host with the API token, in the URL,
// headers and body of an outgoing request, and only for allowed hosts.
const SecretPlaceholder = "{{secret.token}}"

// Manifest is manifest.json at the root of an extension zip.
type Manifest struct {
	// ID is a stable slug (lowercase letters, digits, dashes). Uploading a zip
	// with an ID that is already installed replaces that extension.
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Version      string    `json:"version"`
	APIVersion   int       `json:"api_version"`
	Description  string    `json:"description,omitempty"`
	Capabilities []string  `json:"capabilities"`
	Settings     []Setting `json:"settings"`
	// AllowedHosts are the only hosts http_request may call. An entry
	// "{settings.base_url}" allows the host of the key's connection URL.
	AllowedHosts []string `json:"allowed_hosts"`
	Limits       Limits   `json:"limits"`
	// Entry is the WASM file inside the zip.
	Entry string `json:"entry"`
	// Icon is an optional square PNG inside the zip, shown on the provider's
	// tile.
	Icon string `json:"icon,omitempty"`
}

// Setting is one field the admin fills in on the settings page.
type Setting struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Secret  bool   `json:"secret,omitempty"`
	Default string `json:"default,omitempty"`
}

// Limits tell the server how to call the extension.
type Limits struct {
	// BatchSize is the most rows sent in one predict call.
	BatchSize int `json:"batch_size,omitempty"`
	// RequestsPerSecond caps the extension's outgoing HTTP requests per second
	// for one key, shared by every call running at once in a server process.
	RequestsPerSecond int `json:"requests_per_second,omitempty"`
}

// Context is sent with every call. Settings holds the non-secret settings
// (base_url); the token is never included.
type Context struct {
	SourceLang string            `json:"source_lang"`
	TargetLang string            `json:"target_lang"`
	Settings   map[string]string `json:"settings,omitempty"`
}

// PredictRequest is the input of predict.
type PredictRequest struct {
	Context Context      `json:"context"`
	Meta    PredictMeta  `json:"meta"`
	Rows    []PredictRow `json:"rows"`
}

// PredictMeta says where a batch comes from. A file is sent in batches of
// at most the manifest's batch_size; BatchIndex is 0-based.
type PredictMeta struct {
	FileID      string `json:"file_id"`
	FileName    string `json:"file_name"`
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
	// StepName is the workflow step running the review, as named in the TMS.
	StepName   string `json:"step_name"`
	BatchIndex int    `json:"batch_index"`
	BatchCount int    `json:"batch_count"`
}

// PredictRow is one segment to score. Inline tags are encoded as {1}, {/1},
// {2/} placeholders; a postedit must keep them.
type PredictRow struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	// Origin is "mt" for machine translation or "tm" for a memory match.
	Origin string `json:"origin"`
}

// PredictResponse is the output of predict. Rows may be in any order and may
// leave a row out; a missing row is left for the human reviewer.
type PredictResponse struct {
	Rows []PredictResult `json:"rows"`
}

// PredictResult is the verdict for one row.
type PredictResult struct {
	ID       string `json:"id"`
	Approved bool   `json:"approved"`
	// Postedit replaces the translation when set (ape capability only).
	Postedit *string `json:"postedit,omitempty"`
	// PosteditApproved approves the postedit. Approved is ignored when a
	// postedit is returned.
	PosteditApproved *bool `json:"postedit_approved,omitempty"`
	// Score is an optional 0-100 quality score shown to the reviewer.
	Score *int `json:"score,omitempty"`
	// Comment is an optional note shown to the reviewer.
	Comment *string `json:"comment,omitempty"`
}

// CheckKeyRequest is the input of check_key: it should make the cheapest
// authenticated request the vendor offers. Return an error for a bad key.
type CheckKeyRequest struct {
	Context Context `json:"context"`
}

// HTTPRequest is what an extension passes to the host's http_request.
type HTTPRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"body,omitempty"`
}

// HTTPResponse is what http_request returns. Error is set when the host
// refused or could not send the request; Status is then 0.
type HTTPResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"body,omitempty"`
	Error   string            `json:"error,omitempty"`
}
