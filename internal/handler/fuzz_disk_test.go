package handler

import (
	"encoding/json"
	"testing"
)

// FuzzAddDiskRequest fuzzes JSON decoding and validation of AddDiskRequest.
func FuzzAddDiskRequest(f *testing.F) {
	seed := []string{
		`{"path":"/extra.raw","readonly":true}`,
		`{}`,
		`{"path":""}`,
		`{"path":"/disk.qcow2","direct":true}`,
		`not-json`,
		`{"path":123}`,
		`{"path":"/dev/sda","readonly":false,"direct":false}`,
	}
	for _, s := range seed {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var req AddDiskRequest
		_ = json.Unmarshal(data, &req)
		_ = req.Validate()
	})
}
