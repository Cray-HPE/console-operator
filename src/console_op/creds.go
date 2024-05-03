//
//  MIT License
//
//  (C) Copyright 2020-2024 Hewlett Packard Enterprise Development LP
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

// This file contains the functions to configure and interact with conman

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/tidwall/gjson"
)

// Location of the Mountain BMC console ssh key pair files.
// These are obtained or generated when the pod is created.
const mountainConsoleKey string = "/var/log/console/conman.key"
const mountainConsoleKeyPub string = "/var/log/console/conman.key.pub"

// Location of the Kubernetes service account token used to authenticate
// to Vault.  This is part of the pod deployment.
const svcAcctTokenFile string = "/var/run/secrets/kubernetes.io/serviceaccount/token"

// The Vault base URI
const vaultBase = "http://cray-vault.vault:8200/v1"

// The Vault specific secret name of the Conman Mountain BMC console private key.
// If this secret does not exist Vault will be asked to create it.
const vaultBmcKeyName = "mountain-bmc-console"

// The Vault key type used when generating a new key intended for use with
// Mountain console ssh.
const vaultBmcKeyAlg = "rsa-2048"

// Struct to hold the overall scsd response
type scsdList struct {
	Targets []scsdNode `json:"Targets"`
}

// Struct to hold the individual scsd node status
type scsdNode struct {
	Xname      string `json:"Xname"`
	StatusCode int    `json:"StatusCode"`
	StatusMsg  string `json:"StatusMsg"`
}

// Ask Vault to generate a private key.  This method is called when it is necessary
// to have Vault create the key when it is missing or to enable future support
// for key rotation.  When a future REST api is added to support Conman operations
// this method should provide the backing support for key rotation.
func vaultGeneratePrivateKey(vaultToken string) (response []byte, responseCode int, err error) {
	// Create the parameters
	vaultParam := map[string]string{
		"type":       vaultBmcKeyAlg,
		"exportable": "true",
	}
	jsonVaultParam, err := json.Marshal(vaultParam)
	log.Printf("Preparing to ask Vault to generate the key with the parameters:\n %s",
		string(jsonVaultParam))
	if err != nil {
		return response, responseCode, err
	}

	// Tell vault to create the private key
	URL := vaultBase + "/transit/keys/" + vaultBmcKeyName
	vaultRequestHeaders := make(map[string]string)
	vaultRequestHeaders["X-Vault-Token"] = vaultToken
	response, responseCode, err = postURL(URL, jsonVaultParam, vaultRequestHeaders)

	// Return any general error.
	if err != nil {
		return response, responseCode, err
	}

	if responseCode != 204 {
		// Return an error for any unhandled http response code.
		log.Printf(
			"Unexpected response from Vault when generating the key: %s  Http response code: %d",
			response, responseCode)
		return response, responseCode, fmt.Errorf(
			"Unexpected response from Vault when generating the key: %s  Http response code: %d",
			response, responseCode)
	}

	log.Printf("A new secret for %s was generated in vault.", vaultBmcKeyName)
	return response, responseCode, nil
}

// Ask vault for the private key
func vaultExportPrivateKey(vaultToken string) (pvtKey string, response []byte, responseCode int, err error) {
	URL := vaultBase + "/transit/export/signing-key/" + vaultBmcKeyName
	vaultRequestHeaders := make(map[string]string)
	vaultRequestHeaders["X-Vault-Token"] = vaultToken
	response, responseCode, err = getURL(URL, vaultRequestHeaders)
	// Handle any general error with the request.
	if err != nil {
		log.Printf(
			"Unable to get the %s secret from vault: %s  Error was: %s",
			vaultBmcKeyName, vaultBase, err)
		return "", response, responseCode, fmt.Errorf("Unable to get the %s secret from vault: %s  Error was: %s",
			vaultBmcKeyName, vaultBase, err)
	}

	if responseCode == 404 {
		log.Printf("The vault secret %s was not found. It will need to be created.", vaultBmcKeyName)

		return "", response, 404, nil
	} else if responseCode == 200 {
		// Return the secret we found
		jsonElem := "data.keys.1" // See https://github.com/tidwall/gjson#path-syntax
		pvtKey := gjson.Get(string(response), jsonElem)
		if len(pvtKey.String()) == 0 {
			log.Printf(
				"Empty or missing %s element in Vault response",
				jsonElem)
			return "", response, responseCode, fmt.Errorf("Empty or missing %s element in Vault response",
				jsonElem)
		}
		return pvtKey.String(), response, 200, nil
	} else {
		// Return an error for any unhandled http response code.
		log.Printf(
			"Unexpected response from Vault: %s  Http response code: %d",
			response, responseCode)
		return "", response, responseCode, fmt.Errorf("Unexpected response from Vault: %s  Http response code: %d",
			response, responseCode)
	}
}

// Obtain the private key from Vault.  The private key (aka Vault secret) is the
// only piece of the key pair which is stored in Vault.  The public key piece is
// created from the private via the standard ssh-keygen utility.
// If the private key can not be found then vault will be asked to generate and
// return the new key.
func vaultGetPrivateKey(vaultToken string) (pvtKey string, err error) {
	// Ask vault for the existing key
	pvtKey, response, responseCode, err := vaultExportPrivateKey(vaultToken)
	if err != nil {
		return "", err
	}

	if responseCode == 200 {
		// Return the private key that was found in vault.
		return pvtKey, nil
	} else if responseCode == 404 {
		// Ask vault to generate a private key.
		response, responseCode, err := vaultGeneratePrivateKey(vaultToken)
		if err != nil {
			return "", err
		}

		// Handle any unexpected http error when generating the key.
		if responseCode != 204 {
			return "", fmt.Errorf(
				"Unexpected response from Vault when generating the key: %s  Http response code: %d",
				response, responseCode)
		}

		// Ask vault again to export the newly generated private key.
		pvtKey, response, responseCode, err = vaultExportPrivateKey(vaultToken)
		if err != nil {
			return "", err
		}
		if responseCode != 200 {
			return "", fmt.Errorf(
				"Unexpected response from Vault when requesting the key: %s  Http response code: %d",
				response, responseCode)
		}

		// Return the private key that was found in vault.
		return pvtKey, nil

	} else {
		// Handle an unexpected http response when initially requesting the key.
		return "", fmt.Errorf(
			"Unexpected response from Vault when requesting the key: %s  Http response code: %d",
			response, responseCode)
	}
}

// Obtain Mountain node BMC credentials from Vault and stage them to the
// local file system.  A specific error will be returned in the event of
// any issues.
func vaultGetMountainConsoleCredentials() error {
	// Generate an ssh key pair (/etc/conman.key and /etc/conman.key.pub)
	// This will overwrite the existing public or private key files.

	// Authenticate to Vault
	svcAcctToken, err := ioutil.ReadFile(svcAcctTokenFile)
	if err != nil {
		log.Printf("Unable to read the service account token file: %s  Can not authenticate to vault.", err)
		return fmt.Errorf("Unable to read the service account token file: %s can not authenticate to vault", err)
	}

	vaultAuthParam := map[string]string{
		"jwt":  string(svcAcctToken),
		"role": "ssh-user-certs-compute"}
	jsonVaultAuthParam, _ := json.Marshal(vaultAuthParam)
	URL := vaultBase + "/auth/kubernetes/login"
	log.Printf("Attempting to authenticate to Vault at: %s", URL)
	response, responseCode, err := postURL(URL, jsonVaultAuthParam, nil)
	if err != nil {
		log.Printf("Unable to authenticate to Vault: %s", err)
		return fmt.Errorf("Unable to authenticate to Vault: %s", err)
	}
	// If the response code is not 200 then we failed authentication.
	if responseCode != 200 {
		log.Printf(
			"Vault authentication failed.  Response code: %d  Message: %s",
			responseCode, string(response))
		return fmt.Errorf(
			"Vault authentication failed.  Response code: %d  Message: %s",
			responseCode, string(response))
	}
	log.Printf("Vault authentication was successful.  Attempting to get BMC console key from vault")
	vaultToken := gjson.Get(string(response), "auth.client_token")

	// Get the private key from Vault.
	pvtKey, err := vaultGetPrivateKey(vaultToken.String())
	if err != nil {
		return err
	}
	log.Printf("Obtained BMC console key from vault.")

	// Write the private key to the local file system.
	err = ioutil.WriteFile(mountainConsoleKey, []byte(pvtKey), 0600)
	if err != nil {
		log.Printf("Failed to write our the private ssh key received from Vault.")
		return err
	}

	// Extract the public key from the private and convert to ssh format.
	log.Printf("Attempting to obtain BMC public console key.")
	var outBuf bytes.Buffer
	cmd := exec.Command("sh", "-c", fmt.Sprintf("ssh-keygen -yf %s > %s",
		mountainConsoleKey, mountainConsoleKeyPub))
	cmd.Stderr = &outBuf
	cmd.Stdout = &outBuf
	err = cmd.Run()
	if err != nil {
		log.Printf("Error extracting the public key: %s", err)
		return err
	}
	log.Printf("Successfully obtained BMC public console key.")
	return nil // no error
}

// Used to generate Mountain console credentials in the event
// they can not be provided by Vault.
func generateMountainConsoleCredentials() error {
	// TODO: we should be able to call this directly and eliminate the script - need
	//  to figure out why this isn't working as expected
	//cmd := exec.Command("/usr/bin/ssh-keygen", "-qf", mountainConsoleKey, "-N", "''", "<<<y")
	// Error code 1 ...

	// Generate an ssh key pair (/etc/conman.key and /etc/conman.key.pub)
	// This will overwrite the existing public or private key files.
	var outBuf bytes.Buffer
	cmd := exec.Command("/app/console-ssh-keygen")
	cmd.Stderr = &outBuf
	cmd.Stdout = &outBuf
	err := cmd.Run()
	if err != nil {
		log.Printf("Error generating console key pair: %s", err)
		return fmt.Errorf("Error generating console key pair: %s", err)
	}
	return nil
}

// Ensure that Mountain node console credentials have been generated.
func ensureMountainConsoleKeysExist() bool {
	// if running in debug mode there won't be any nodes or vault present
	if debugOnly {
		log.Print("Running in debug mode - skipping mountain cred generation")
		return true
	}

	// Check that we have key pair files on local storage
	_, errKey := os.Stat(mountainConsoleKey)
	_, errPub := os.Stat(mountainConsoleKeyPub)
	if os.IsNotExist(errKey) || os.IsNotExist(errPub) {
		// does not exist
		log.Printf("Obtaining Mountain console credentials from Vault")
		if err := vaultGetMountainConsoleCredentials(); err != nil {
			log.Printf("%s", err)
			log.Printf("Generating Mountain console credentials.")
			if err := generateMountainConsoleCredentials(); err != nil {
				log.Printf("Unable to generate credentials.  Error was: %s", err)
				return false
			}
		}
	}
	return true
}

// Watches the mountainCredsUpdateChannel for new nodes to update
func doMountainCredsUpdates(mountainCredsUpdateChannel chan nodeConsoleInfo) {
	nodesToUpdate := make(map[string]nodeConsoleInfo)
	for {
		select {
		case node := <-mountainCredsUpdateChannel:
			nodesToUpdate[node.NodeName] = node
		case <-time.After(time.Second):
			// If no new nodes come in for 1 second, send the current batch
			updateCount := len(nodesToUpdate)
			if updateCount > 0 {
				log.Printf("Updating mountain keys for %d nodes", updateCount)
				nodesToUpdate = doMountainCredsUpdate(nodesToUpdate)
				remainingCount := len(nodesToUpdate)
				if remainingCount > 0 {
					log.Printf("%d out of %d key updates failed and will be retried", remainingCount, updateCount)
					// Sleep for 1 minute so we don't flood the system/logs with retries
					time.Sleep(60 * time.Second)
				} else {
					log.Printf("All key updates succeeded")
				}
			}
		}
	}
}

// Takes a list of mountain nodes to update and returns a list of nodes that failed and need to be retried
func doMountainCredsUpdate(nodesToUpdate map[string]nodeConsoleInfo) (remaining map[string]nodeConsoleInfo) {
	nodeList := make([]nodeConsoleInfo, len(nodesToUpdate))
	bmcMap := make(map[string][]string)
	for nodeKey, node := range nodesToUpdate {
		nodeList = append(nodeList, node)
		bmcMap[node.BmcName] = append(bmcMap[node.BmcName], nodeKey)
	}
	success, reply := deployMountainConsoleKeys(nodeList)
	if !success {
		return nodesToUpdate
	}
	for _, t := range reply.Targets {
		if t.StatusCode == 204 {
			// BMC update was successful and all associated nodes can be removed from the update list
			for _, xname := range bmcMap[t.Xname] {
				delete(nodesToUpdate, xname)
			}
		}
	}
	log.Printf("remaining: %d", len(nodesToUpdate))
	return nodesToUpdate
}

// Deploy mountain node console credentials.
func deployMountainConsoleKeys(nodes []nodeConsoleInfo) (bool, scsdList) {
	// Ensure that we have a console ssh key pair.  If the key pair
	// is not on the local file system then obtain it from Vault.  If
	// Vault is not available or we are otherwise unable to obtain the key
	// pair then generate it and log a message.  We want to minimize any
	// loss of console logs or console access due to a missing ssh
	// key pair.
	scsdReply := scsdList{}

	// if running in debug mode there won't be any nodes or vault present
	if debugOnly {
		log.Print("Running in debug mode - skipping mountain cred generation")
		return true, scsdReply
	}

	// Read in the public key.
	pubKey, err := ioutil.ReadFile(mountainConsoleKeyPub)
	if err != nil {
		log.Printf("Unable to read the public key file: %s", err)
		return false, scsdReply
	}

	// Obtain the list of Mountain bmcs from the node list.
	// Note there are two nodes per bmc and one update per bmc
	// is all that is required to set the ssh console key for
	// both nodes.
	mtnBmcList := make(map[string]string)
	for _, nodeCi := range nodes {
		if nodeCi.isMountain() {
			mtnBmcList[nodeCi.BmcFqdn] = nodeCi.BmcName
		}
	}
	mtnNodeBmcArray := make([]string, 0, len(mtnBmcList))
	for bmcName := range mtnBmcList {
		mtnNodeBmcArray = append(mtnNodeBmcArray, bmcName)
	}

	// Create an HMS scsd json structure containing the Mountain BMC list and
	// the public key to deploy.
	scsdParam := map[string]interface{}{
		"Targets": mtnNodeBmcArray,
		"Params": map[string]string{
			"SSHConsoleKey": string(pubKey),
		},
		"Force": false,
	}
	jsonScsdParam, _ := json.Marshal(scsdParam)
	log.Printf("Preparing to call scsd with the parameters:\n %s", string(jsonScsdParam))

	// Call the HMS scsd service to deploy the public key.
	log.Print("Calling scsd to deploy Mountain BMC ssh key(s)")
	URL := "http://cray-scsd/v1/bmc/loadcfg"
	data, rc, _ := postURL(URL, jsonScsdParam, nil)

	// consider any http return code < 400 as success
	success := rc < 300

	// parse the return data

	err = json.Unmarshal(data, &scsdReply)
	if err != nil {
		log.Printf("Error unmarshalling the reply from scsd: %s", err)
		return success, scsdReply
	}
	for _, t := range scsdReply.Targets {
		if t.StatusCode != 204 {
			log.Printf("scsd FAILED to deploy ssh key to BMC: %s -> %d %s", t.Xname, t.StatusCode, t.StatusMsg)
		} else {
			log.Printf("scsd deployed ssh console key to: %s", t.Xname)
		}
	}
	// TBD - Beyond just logging the status, determine if there is a more preferred way
	// to deal with any specific failures to deploy a BMC ssh console key.
	// Scsd response example:
	//  {"Xname":"x5000c1s2b0","StatusCode":204,"StatusMsg":"OK"}
	// Example errors:
	//  {"Xname":"x5000c2s5b0","StatusCode":422,"StatusMsg":"Target 'x5000c2s5b0' in bad HSM state: Unknown"}
	//  {"Xname":"x5000c3r1b0","StatusCode":500,"StatusMsg":"Internal Server Error"}
	//
	// In addition perhaps we want to keep a map (map[string]string) of hostname to
	// public key as a record of the deployment success or errors on a per
	// BMC and public key basis.  This could be used in the future to reduce the time
	// to redeploy all keys.

	return success, scsdReply
}
