//go:build wasip1

// Command mockqe is a test extension. With no base_url it decides locally:
// a target containing "BAD" is rejected, "FIXME" is post-edited to "fixed"
// and approved, anything else is approved. With a base_url it forwards the
// rows to {base_url}/predict?token=… and returns the server's answer.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"openloc/pkg/extsdk"
)

func main() {}

//go:wasmexport predict
func predict() int32 { return extsdk.Handle(runPredict) }

//go:wasmexport check_key
func checkKey() int32 { return extsdk.Handle(runCheckKey) }

func runPredict(in extsdk.PredictRequest) (extsdk.PredictResponse, error) {
	if base := in.Context.Settings[extsdk.SettingBaseURL]; base != "" {
		return remote(base, in)
	}
	out := extsdk.PredictResponse{Rows: make([]extsdk.PredictResult, 0, len(in.Rows))}
	for _, r := range in.Rows {
		res := extsdk.PredictResult{ID: r.ID, Approved: !strings.Contains(r.Target, "BAD")}
		if strings.Contains(r.Target, "FIXME") {
			pe := strings.ReplaceAll(r.Target, "FIXME", "fixed")
			ok := true
			res.Postedit, res.PosteditApproved = &pe, &ok
		}
		out.Rows = append(out.Rows, res)
	}
	return out, nil
}

func remote(base string, in extsdk.PredictRequest) (extsdk.PredictResponse, error) {
	var out extsdk.PredictResponse
	body, err := json.Marshal(in.Rows)
	if err != nil {
		return out, err
	}
	resp, err := extsdk.HTTP(extsdk.HTTPRequest{
		Method:  "POST",
		URL:     base + "/predict?token=" + extsdk.SecretPlaceholder,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    body,
	})
	if err != nil {
		return out, err
	}
	if resp.Status != 200 {
		return out, fmt.Errorf("predict: status %d", resp.Status)
	}
	return out, json.Unmarshal(resp.Body, &out)
}

func runCheckKey(in extsdk.CheckKeyRequest) (struct{}, error) {
	base := in.Context.Settings[extsdk.SettingBaseURL]
	if base == "" {
		return struct{}{}, nil
	}
	resp, err := extsdk.HTTP(extsdk.HTTPRequest{Method: "GET", URL: base + "/check?token=" + extsdk.SecretPlaceholder})
	if err != nil {
		return struct{}{}, err
	}
	if resp.Status != 200 {
		return struct{}{}, errors.New("the token was rejected")
	}
	return struct{}{}, nil
}
