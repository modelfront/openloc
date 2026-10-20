//go:build wasip1

package extsdk

import (
	"encoding/json"
	"errors"

	"github.com/extism/go-pdk"
)

//go:wasmimport extism:host/user http_request
func hostHTTPRequest(offset uint64) uint64

// HTTP sends req through the host. Put SecretPlaceholder where the API token
// belongs; the host fills it in.
func HTTP(req HTTPRequest) (HTTPResponse, error) {
	var resp HTTPResponse
	mem, err := pdk.AllocateJSON(req)
	if err != nil {
		return resp, err
	}
	defer mem.Free()
	out := pdk.FindMemory(hostHTTPRequest(mem.Offset()))
	defer out.Free()
	if err := json.Unmarshal(out.ReadBytes(), &resp); err != nil {
		return resp, err
	}
	if resp.Error != "" {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}

// Handle reads the JSON input into In, runs fn and writes its result as the
// JSON output. It returns the status code for a //go:wasmexport function.
func Handle[In, Out any](fn func(In) (Out, error)) int32 {
	var in In
	if err := pdk.InputJSON(&in); err != nil {
		pdk.SetError(err)
		return 1
	}
	out, err := fn(in)
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	if err := pdk.OutputJSON(out); err != nil {
		pdk.SetError(err)
		return 1
	}
	return 0
}
