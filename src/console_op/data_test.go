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

func (K8GetPodLocationMock) getPodLocationAlias(podID string) (loc string, err error) {
	return "node-foo", nil
}

type SlsGetXnameAliasesMock struct {
	SlsManager
}

func (SlsGetXnameAliasesMock) getXnameAlias() (xnameNodeAlias []XnameNodeAlias, err error) {
	mock := []XnameNodeAlias{}
	mock = append(mock, XnameNodeAlias{xname: "x3000c0s17b1", alias: "node-foo"})
	return mock, nil
}

func TestDoGetPodLocation(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/console-operator/v1/location/{podID}", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("podID", "pod-1234")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	dm := NewDataManager(K8GetPodLocationMock{}, SlsGetXnameAliasesMock{})
	handler := http.HandlerFunc(dm.doGetPodLocation)
	handler.ServeHTTP(rr, req)

	// Expected results
	eAlias := "node-foo"
	eName := "pod-1234"
	eXname := "x3000c0s17b1"

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler returned incorrect status code. Expected: %d Got: %d", http.StatusOK, status)
	}

	var resp PodLocationDataResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Errorf("Error decoding response body: %v", err)
	}

	if resp.Alias != eAlias {
		t.Errorf("Expected: %s. Got: %s.", eAlias, resp.Alias)
	}
	if resp.PodName != eName {
		t.Errorf("Expected: %s. Got: %s.", eName, resp.PodName)
	}
	if resp.Xname != eXname {
		t.Errorf("Expected: %s. Got: %s.", eXname, resp.Xname)
	}
}

type K8GetReplicaCountMock struct {
	// embed this so only mock methods as needed
	K8Manager
}

func (K8GetReplicaCountMock) getReplicaCount() (repCount int, err error) {
	return 3, nil
}

func TestDoGetPodReplicaCount(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/console-operator/v1/replicas", nil)

	// Expected results
	eReplicas := 3

	dm := NewDataManager(K8GetReplicaCountMock{}, SlsGetXnameAliasesMock{})
	handler := http.HandlerFunc(dm.doGetPodReplicaCount)
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler returned incorrect status code. Expected: %d Got: %d", http.StatusOK, status)
	}

	var resp GetNodeReplicasResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Errorf("Error decoding response body: %v", err)
	}

	if resp.Replicas != eReplicas {
		t.Errorf("Expected: %d. Got: %d.", eReplicas, resp.Replicas)
	}
}
