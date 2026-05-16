package handler

import (
	"encoding/json"
	"testing"
)

// FuzzAddInterfaceRequest fuzzes JSON decoding and validation of AddInterfaceRequest.
func FuzzAddInterfaceRequest(f *testing.F) {
	seed := []string{
		`{"tap":"tap0","ip":"10.0.0.2","mac":"02:00:00:00:00:01"}`,
		`{}`,
		`{"mac":"not-a-mac"}`,
		`{"ip":"not-an-ip"}`,
		`{"tap":""}`,
		`not-json`,
		`{"mac":"aa:bb:cc:dd:ee:ff"}`,
		`{"ip":"192.168.1.0/24"}`,
	}
	for _, s := range seed {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var req AddInterfaceRequest
		_ = json.Unmarshal(data, &req)
		_ = req.Validate()
	})
}

// FuzzPatchInterfaceRequest fuzzes JSON decoding and validation of PatchInterfaceRequest.
func FuzzPatchInterfaceRequest(f *testing.F) {
	seed := []string{
		`{"ip":"192.168.1.10"}`,
		`{}`,
		`{"mac":"bad-mac"}`,
		`{"ip":"invalid"}`,
		`{"tap":"tap1","ip":"10.0.0.5","mac":"00:11:22:33:44:55"}`,
		`not-json`,
		`{"mask":"255.255.255.0"}`,
	}
	for _, s := range seed {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var req PatchInterfaceRequest
		_ = json.Unmarshal(data, &req)
		_ = req.Validate()
	})
}
