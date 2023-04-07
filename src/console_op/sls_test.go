//
//  MIT License
//
//  (C) Copyright 2023 Hewlett Packard Enterprise Development LP
//
//  Permission is hereby granted, free of charge, to any person obtaining a
//  copy of this software and associated documentation files (the "Software"),
//  to deal in the Software without restriction, including without limitation
//  the rights to use, copy, modify, merge, publish, distribute, sublicense,
//  and/or sell copies of the Software, and to permit persons to whom the
//  Software is furnished to do so, subject to the following conditions:
//
//  The above copyright notice and this permission notice shall be included
//  in all copies or substantial portions of the Software.
//
//  THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
//  IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
//  FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL
//  THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR
//  OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE,
//  ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
//  OTHER DEALINGS IN THE SOFTWARE.
//

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// cray-sls/hardware GET mock
var slsHardwareMock = `[
	{
        "Parent": "x3000c0s17b1",
        "Xname": "x3000c0s17b1n0",
        "Type": "comptype_node",
        "Class": "River",
        "TypeString": "Node",
        "LastUpdated": 1672845536,
        "LastUpdatedTime": "2023-01-04 15:18:56.096894 +0000 +0000",
        "ExtraProperties": {
            "Aliases": [
                "nid000001"
            ],
            "NID": 1,
            "Role": "Compute"
        }
    },
    {
        "Parent": "x3000c0w14",
        "Xname": "x3000c0w14j25",
        "Type": "comptype_mgmt_switch_connector",
        "Class": "River",
        "TypeString": "MgmtSwitchConnector",
        "LastUpdated": 1672845536,
        "LastUpdatedTime": "2023-01-04 15:18:56.096894 +0000 +0000",
        "ExtraProperties": {
            "NodeNics": [
                "x3000c0s2b0"
            ],
            "VendorName": "1/1/25"
        }
    },
    {
        "Parent": "x3000c0w14",
        "Xname": "x3000c0w14j36",
        "Type": "comptype_mgmt_switch_connector",
        "Class": "River",
        "TypeString": "MgmtSwitchConnector",
        "LastUpdated": 1672845536,
        "LastUpdatedTime": "2023-01-04 15:18:56.096894 +0000 +0000",
        "ExtraProperties": {
            "NodeNics": [
                "x3000c0s17b1"
            ],
            "VendorName": "1/1/36"
        }
    },
    {
        "Parent": "x3000c0s19b0",
        "Xname": "x3000c0s19b0n0",
        "Type": "comptype_node",
        "Class": "River",
        "TypeString": "Node",
        "LastUpdated": 1672845536,
        "LastUpdatedTime": "2023-01-04 15:18:56.096894 +0000 +0000",
        "ExtraProperties": {
            "Aliases": [
                "uan01"
            ],
            "Role": "Application",
            "SubRole": "UAN"
        }
    }
]`

func TestGetXnameAliases(t *testing.T) {
	// create test server
	hardware := "/hardware"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != hardware {
			t.Errorf("Expected to request %s, got: %s", hardware, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(slsHardwareMock))
	}))
	defer server.Close()

	// override constructor
	slsManager := SlsManager{baseUrl: server.URL}
	expLen := 2 //total of 4 structs, 2 valid
	xnameAlias, _ := slsManager.getXnameAlias()
	actualLen := len(xnameAlias)
	if actualLen != expLen {
		t.Errorf("Expected %d xnameAlias structs, got %d instead", expLen, actualLen)
	}
}
