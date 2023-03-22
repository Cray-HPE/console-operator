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

// Either implement a regex table pattern similar to console-data router (untested)
// to allow for handling URL params with std library, or use a router
// library with no external deps like chi

// Regex table pattern: https://github.com/Cray-HPE/console-data/blob/develop/console_data_svc/router.go

package main

import (
	"github.com/go-chi/chi/v5"
)

var router = chi.NewRouter()

func setupRoutes(ds DataService, hs HealthService, dbs DebugService) {
	// k8s routes
	router.Get("/console-operator/liveness", hs.doLiveness)
	router.Get("/console-operator/readiness", hs.doReadiness)
	router.Get("/console-operator/health", hs.doHealth)

	// debug only routes
	router.Get("/console-operator/info", dbs.doInfo)
	router.Delete("/console-operator/clearData", dbs.doClearData)
	router.Post("/console-operator/suspend", dbs.doSuspend)
	router.Post("/console-operator/resume", dbs.doResume)
	router.Patch("/console-operator/v0/setMaxNodesPerPod", dbs.doSetMaxNodesPerPod)
	router.Get("/console-operator/v0/getNodePod", ds.doGetNodePod)

	// v1
	router.Get("/console-operator/v1/location/{podID}", ds.doGetPodLocation)
	router.Get("/console-operator/v1/replicas", ds.doGetPodReplicaCount)
}
