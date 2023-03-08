// MIT License
//
// (C) Copyright 2023 Hewlett Packard Enterprise Development LP
//
// Permission is hereby granted, free of charge, to any person obtaining a
// copy of this software and associated documentation files (the "Software"),
// to deal in the Software without restriction, including without limitation
// the rights to use, copy, modify, merge, publish, distribute, sublicense,
// and/or sell copies of the Software, and to permit persons to whom the
// Software is furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included
// in all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL
// THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR
// OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE,
// ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
// OTHER DEALINGS IN THE SOFTWARE.
package main

// TODO: move this out of console-op into either new repo or new go package

import (
	"encoding/json"
	"log"
)

type SlsService interface {
	getXnameAlias() (xnameNodeAlias []XnameNodeAlias, err error)
}

// implements SlsService
type SlsManager struct {
	baseUrl string
}

func NewSlsManager() SlsService {
	return &SlsManager{baseUrl: "http://cray-sls/v1"}
}

// https://github.com/Cray-HPE/hms-sls/blob/87f0f0aee95ad5ae1a36b99b787b266bc044fc47/pkg/sls-common/types.go#L46
// type GenericHardware struct {
// 	Parent             string             `json:"Parent"`
// 	Children           []string           `json:"Children,omitempty"`
// 	Xname              string             `json:"Xname"`
// 	Type               HMSStringType      `json:"Type"`
// 	Class              CabinetType        `json:"Class"`
// 	TypeString         xnametypes.HMSType `json:"TypeString"`
// 	LastUpdated        int64              `json:"LastUpdated,omitempty"`
// 	LastUpdatedTime    string             `json:"LastUpdatedTime,omitempty"`
// 	ExtraPropertiesRaw interface{}        `json:"ExtraProperties,omitempty"`
// 	VaultData          interface{}        `json:"VaultData,omitempty"`
// }

// represents node alias and xname mapping
type XnameNodeAlias struct {
	xname string
	alias string
}

// Get node xname data from hms-sls
// Refactor to struct Unmarshal if other fields are needed
func (sls SlsManager) getXnameAlias() (xnameNodeAlias []XnameNodeAlias, err error) {
	hwUrl := sls.baseUrl + "/hardware"
	data, _, err := getURL(hwUrl, nil)
	if err != nil {
		log.Printf("Error: GET %s to hms-sls failed %s\n", hwUrl, err)
		return nil, err
	}

	// Decode to a map since big nested structs from sls
	var slsRespMap []map[string]interface{}
	xnameAlias := []XnameNodeAlias{}
	json.Unmarshal(data, &slsRespMap)

	for _, element := range slsRespMap {
		var aliases []interface{}
		xname := element["Xname"].(string)

		// parse and find Aliases
		if _, ok := element["ExtraProperties"]; ok {
			epMap := element["ExtraProperties"].(map[string]interface{})
			if value, ok := epMap["Aliases"].([]interface{}); ok {
				aliases = value
			}
		}

		if xname != "" && aliases != nil && len(aliases) != 0 {
			xnameAlias = append(xnameAlias, XnameNodeAlias{xname: xname, alias: aliases[0].(string)})
		}
	}
	return xnameAlias, nil
}
