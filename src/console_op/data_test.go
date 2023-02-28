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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

type K8GetPodLocationMock struct {
	// embed this so only mock methods as needed
	K8Manager
}

func (K8GetPodLocationMock) getPodLocation(podID string) (loc string, err error) {
	return "node-foo", nil
}

func TestDoGetPodLocation(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/console-operator/v1/location/{podID}", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("podID", "pod-1234")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	dm := NewDataManager(K8GetPodLocationMock{})
	handler := http.HandlerFunc(dm.doGetPodLocation)
	handler.ServeHTTP(rr, req)

	// Expected results
	eNode := "node-foo"
	eName := "pod-1234"

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler returned incorrect status code. Expected: %d Got: %d", http.StatusOK, status)
	}

	var resp PodLocationDataResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Errorf("Error decoding response body: %v", err)
	}

	if resp.Node != eNode {
		t.Errorf("Expected: %s. Got: %s.", eNode, resp.Node)
	}
	if resp.PodName != eName {
		t.Errorf("Expected: %s. Got: %s.", eName, resp.PodName)
	}
}
