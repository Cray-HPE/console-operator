//
//  MIT License
//
//  (C) Copyright 2025 Hewlett Packard Enterprise Development LP
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

// Interactions with the consoles

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	v1 "k8s.io/api/core/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/scheme"
)

// Header key names for api options
var consoleHeaderTailKey string = "Cray-Tail"
var consoleHeaderDumpOnlyKey string = "Cray-Dump-Only"
var tenantHeaderKey string = "Cray-Tenant-Name"

// DebugLog - set up debug logging if the env variable is set
var DebugLog bool = os.Getenv("DebugLog") == "TRUE"

func writeDebugLog(format string, v ...any) {
	if DebugLog {
		log.Printf(format, v...)
	}
}

// ConsoleService interface for interacting with the consoles themselves
type ConsoleService interface {
	doFollowConsole(w http.ResponseWriter, r *http.Request)
	doInteractConsole(w http.ResponseWriter, r *http.Request)
}

// ConsoleManager implements a ConsoleService
type ConsoleManager struct {
	k8s         K8Service
	dataService DataService
}

// NewConsoleManager factory function to create a new ConsoleService
func NewConsoleManager(k8s K8Service, ds DataService) ConsoleService {
	return &ConsoleManager{k8s: k8s, dataService: ds}
}

// used for upgrading the http connection to a websocket
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

////////////////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////////////////
// IOStreamer
////////////////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////////////////

// IOStreamer - handle the input/output streams from the websocket
type IOStreamer struct {
	// NOTE: for the time being the removal of input from the output steam doesn't work,
	//  but I am going to leave it in place if we get back to trying to fix it
	conn         *websocket.Conn
	mu           sync.Mutex
	inputStrings [][]byte
}

func (l *IOStreamer) addInputString(msg []byte) {
	l.inputStrings = append(l.inputStrings, msg)
}

func (l *IOStreamer) removeInputStrings(msg []byte) []byte {
	// find all input strings in msg and remove them
	retVal := bytes.Clone(msg)
	for i, inStr := range l.inputStrings {
		writeDebugLog("WEBSOCKET::RemoveInputStrings: retVal:%s inStr:%s", string(retVal), string(inStr))
		beforeStr, afterStr, found := bytes.Cut(retVal, inStr)
		if found {
			writeDebugLog("  Found input string")
			// remove the element from the input strings
			l.inputStrings = append(l.inputStrings[:i], l.inputStrings[i+1:]...)

			// concatenate the before and after bits
			if len(beforeStr) > 0 && len(afterStr) > 0 {
				retVal = append(beforeStr, afterStr...)
			} else if len(beforeStr) > 0 {
				retVal = bytes.Clone(beforeStr)
			} else if len(afterStr) > 0 {
				retVal = bytes.Clone(afterStr)
			} else {
				// nothing left
				writeDebugLog("  All input removed")
				return nil
			}
			writeDebugLog("  New retVal:%s", string(retVal))
		} else {
			writeDebugLog("  No input found")
		}
	}

	return retVal
}

func (l *IOStreamer) Read(p []byte) (n int, err error) {
	// NOTE: the intent is to remove the input stream contents from the
	//  output stream so they don't get printed twice. It isn't quite working
	//  yet, but I am leaving it in place for now.
	//l.mu.Lock()
	//defer l.mu.Unlock()

	// Read the next message from the websocket connection
	_, msgArr, err := l.conn.ReadMessage()
	//writeDebugLog("WEBSOCKET::Read Read: type:%d, len:%d, bytes:%s", msgType, len(msgArr), msgArr[:n])

	// keep this message to remove from write stream to avoid double writing
	//l.addInputString(msgArr)

	// The newline gets stripped off by the websocket - add it back
	// NOTE - without this the command will not be executed on the remote terminal
	enhanced := append(msgArr, "\n"...)
	copy(p, enhanced)
	return len(enhanced), err
}

func (l *IOStreamer) String() string {
	// we only care about streaming to the websocket connection - stub this out
	return ""
}

func (l *IOStreamer) writeMessage(msg []byte) error {
	// if there is nothing to write, don't try to write
	if len(msg) == 0 {
		//log.Print("WEBSOCKET::Writing::Message - Empty String")
		return nil
	}
	//writeDebugLog("WEBSOCKET::Writing::Message: %s", string(msg))
	err := l.conn.WriteMessage(websocket.TextMessage, msg)
	if err != nil {
		log.Printf("WEBSOCKET Writing ERROR: %v", err)
	}
	return err
}

func (l *IOStreamer) Write(p []byte) (n int, err error) {
	// NOTE: the intent is to remove the input stream contents from the
	//  output stream so they don't get printed twice. It isn't quite working
	//  yet, but I am leaving it in place for now.
	//l.mu.Lock()
	//defer l.mu.Unlock()

	// remove any input commands so they don't print twice in the output
	inStr := bytes.Clone(p)
	//inStr = l.removeInputStrings(inStr)

	// Process the remaining strings
	n = len(inStr)
	err = l.writeMessage(inStr)

	return n, err
}

func (cs ConsoleManager) validateNode(r *http.Request) (xname, podname, errMsg string, errCode int) {
	// Find the node requested, make sure it is valid, and if there is a tenant validate authorization
	xname = ""
	podname = ""
	errMsg = ""
	errCode = http.StatusOK

	// `/console-operator/interact/{nodeXname}` - pull out the node being interacted with
	xname = chi.URLParam(r, "nodeXname")
	if xname == "" {
		log.Printf("There was an error reading the node xname from the request %s", r.URL.Path)
		errMsg = fmt.Sprintf("There was an error reading the node xname from the request %s", r.URL.Path)
		errCode = http.StatusBadRequest
		return
	}

	// make sure this is a valid node
	if _, ok := nodeCache[xname]; !ok {
		log.Printf("%s is not a valid node.", xname)
		errMsg = fmt.Sprintf("%s is not a valid node.", xname)
		errCode = http.StatusNotFound
		return
	}

	// find which container is monitoring this node
	podname, err := cs.dataService.getNodePodForXname(xname)
	if err != nil {
		log.Printf("Node %s is not being monitored", xname)
		errMsg = fmt.Sprintf("Node %s is not currently being monitored", xname)
		errCode = http.StatusNotFound
		return
	}

	// If the user is part of a tenant, check if the node is allowed
	tenant := r.Header.Get(tenantHeaderKey)
	if tenant != "" && !cs.isTenantAllowed(tenant, xname) {
		log.Printf("Tenant %s is not allowed to access node %s", tenant, xname)
		errMsg = fmt.Sprintf("Tenant %s is not allowed to access node %s", tenant, xname)
		errCode = http.StatusForbidden
		return
	}

	return
}

// Finds and returns the node where the given pod is running within the k8s cluster.
func (cs ConsoleManager) doInteractConsole(w http.ResponseWriter, r *http.Request) {
	// only allow 'GET' calls
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		sendJSONError(w, http.StatusMethodNotAllowed,
			fmt.Sprintf("(%s) Not Allowed", r.Method))
		return
	}

	// Make sure this is a valid operation and all required information is present
	xname, podName, errMsg, errCode := cs.validateNode(r)
	if errCode != http.StatusOK {
		var body = BaseResponse{
			Msg: errMsg,
		}
		SendResponseJSON(w, errCode, body)
		return
	}

	// upgrade https to secure websocket connection
	conn, err := upgrader.Upgrade(w, r, nil)
	defer conn.Close()
	if err != nil {
		log.Printf("Error upgrading http to websocket connection: %v", err)
		return
	}

	// Build the command to be executed in the pod
	cmd := []string{"conman", "-j", xname}

	// Set up the remote request to the pod
	req := cs.k8s.getClientSet().CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace("services").
		SubResource("exec").
		VersionedParams(&v1.PodExecOptions{
			Command:   cmd,
			Container: "cray-console-node",
			Stdin:     true,
			Stdout:    true,
			Stderr:    false,
			TTY:       true,
		}, scheme.ParameterCodec)

	// create the executor to run the command
	config, err := rest.InClusterConfig()
	executor, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		log.Printf("doInteractConsole: failed to create executor: %v", err)
	}

	// Execute the command piping I/O to the IOStreamer class
	webIO := &IOStreamer{conn: conn}
	err = executor.Stream(remotecommand.StreamOptions{
		Stdin:  webIO,
		Stdout: webIO,
		Stderr: nil,
		Tty:    true,
	})
	if err != nil {
		log.Printf("doInteractConsole: failed to execute command in pod: %v", err)
	}
}

// Finds and returns the node where the given pod is running within the k8s cluster.
func (cs ConsoleManager) doFollowConsole(w http.ResponseWriter, r *http.Request) {
	// This is accessed with a connection that can be upgraded to a websocket.

	// only allow 'GET' calls
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		sendJSONError(w, http.StatusMethodNotAllowed,
			fmt.Sprintf("(%s) Not Allowed", r.Method))
		return
	}

	// Make sure this is a valid operation and all required information is present
	xname, podName, errMsg, errCode := cs.validateNode(r)
	if errCode != http.StatusOK {
		var body = BaseResponse{
			Msg: errMsg,
		}
		SendResponseJSON(w, errCode, body)
		return
	}

	// upgrade https to secure websocket connection
	conn, err := upgrader.Upgrade(w, r, nil)
	defer conn.Close()
	if err != nil {
		fmt.Println("Error upgrading:", err)
		return
	}

	// start building the remote command from the input options
	cmd := []string{"tail"}

	// Find if this is following or just dumping the log
	if r.Header.Get(consoleHeaderDumpOnlyKey) != "True" {
		// NOTE: use '-F' so the follow works through a log rotation
		cmd = append(cmd, "-F")
	}

	// Find if there is a number of lines to display
	numLines := r.Header.Get(consoleHeaderTailKey)
	if numLines != "" {
		cmd = append(cmd, "-n", numLines)
	}

	// add the filename to the command
	filename := fmt.Sprintf("/var/log/conman/console.%s", xname)
	cmd = append(cmd, filename)

	// Set up the remote request to the pod
	req := cs.k8s.getClientSet().CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace("services").
		SubResource("exec").
		VersionedParams(&v1.PodExecOptions{
			Command:   cmd,
			Container: "cray-console-node",
			Stdin:     true,
			Stdout:    true,
			Stderr:    false,
			TTY:       true,
		}, scheme.ParameterCodec)

	// create the executor to run the command
	config, err := rest.InClusterConfig()
	executor, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		log.Printf("failed to create executor: %v", err)
	}

	// Execute the command piping I/O to the IOStreamer class
	webIO := &IOStreamer{conn: conn}
	err = executor.Stream(remotecommand.StreamOptions{
		Stdin:  webIO,
		Stdout: webIO,
		Stderr: nil,
		Tty:    true,
	})
	if err != nil {
		log.Printf("WEBSOCKET:: failed to execute command in pod: %v", err)
	}
}

/////////////////////////////////////////////////////////////////////////////
/////////////////////////////////////////////////////////////////////////////
// Tenant authentication
/////////////////////////////////////////////////////////////////////////////
/////////////////////////////////////////////////////////////////////////////

var serviceName string = "cray-tapms/v1alpha3"

// CASMCMS-9125: Currently when TAPMS bumps this version, it
// breaks backwards compatibility, so BOS needs to update this
// whenever TAPMS does.
var baseEndpoint = "http://" + serviceName

// # CASMPET-6433 changed this from tenant to tenants
var tenantEndpoint = baseEndpoint + "/tenants"

// function to find if the node is allowed for the input tenant
func (cs ConsoleManager) isTenantAllowed(tenant string, xname string) bool {
	// construct the uri for getting the tenant information
	uri := fmt.Sprintf("%s/%s", tenantEndpoint, tenant)
	log.Printf("Checking tenant %s for node %s", tenant, xname)

	// make the request to the tenant service
	data, statusCode, err := getURL(uri, nil)
	if err != nil {
		log.Printf("Error calling tapms. rc: %d, error: %s", statusCode, err)
		return true
	}

	// define structs to unmarshal the response
	// NOTE: defined in https://github.com/Cray-HPE/cray-tapms-operator/blob/main/api/v1alpha3/tenant_types.go
	type tenantResourceJSON struct {
		Type                      string   `json:"type" example:"compute" binding:"required"`
		Xnames                    []string `json:"xnames" example:"x0c3s5b0n0,x0c3s6b0n0" binding:"required"`
		HsmPartitionName          string   `json:"hsmpartitionname,omitempty" example:"blue"`
		HsmGroupLabel             string   `json:"hsmgrouplabel,omitempty" example:"green"`
		EnforceExclusiveHsmGroups bool     `json:"enforceexclusivehsmgroups"`
	}
	type tenantStatusJSON struct {
		ChildNamespaces []string             `json:"childnamespaces,omitempty" example:"vcluster-blue-slurm"`
		TenantResources []tenantResourceJSON `json:"tenantresources,omitempty"`
		UUID            string               `json:"uuid,omitempty" example:"550e8400-e29b-41d4-a716-446655440000" format:"uuid"`
	}
	type tenantJSON struct {
		//	Spec TenantSpec `json:"spec,omitempty" binding:"required"`
		Status tenantStatusJSON `json:"status,omitempty"`
	}

	// unmarshal the data into the tenant struct
	var nd tenantJSON
	err = json.Unmarshal(data, &nd)
	if err != nil {
		log.Printf("Error unmarshalling data from tapms: %s", err)
		return true
	}

	// check if the tenant is in the list of allowed tenants
	for _, t := range nd.Status.TenantResources {
		for _, xn := range t.Xnames {
			log.Printf("  Tenant xname: %s", xn)
			if xn == xname {
				return true
			}
		}
	}

	log.Printf("Tenant %s not found for xname %s", tenant, xname)
	return false
}
