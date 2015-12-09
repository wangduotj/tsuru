// Copyright 2015 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package saml

import (
	"time"
	"fmt"
	
	"github.com/tsuru/tsuru/errors"
	"github.com/tsuru/config"
	saml "github.com/diego-araujo/go-saml"
)

var (
	ErrRequestIdNotFound =  &errors.ValidationError{Message: "Field InResponseTo not found in saml response data"} 
	ErrCheckSignature =  &errors.ValidationError{Message: "SAMLResponse signature validation"} 
)

type Response struct {
	ID     string    `json:"id"`
	Creation  time.Time     `json:"creation"`
}

func GetRequestIdFromResponse(r *saml.Response) (string, error){
	
	var idRequest string

	if r.IsEncrypted() {
		idRequest = r.EncryptedAssertion.Assertion.Subject.SubjectConfirmation.SubjectConfirmationData.InResponseTo
	} else {
		idRequest = r.Assertion.Subject.SubjectConfirmation.SubjectConfirmationData.InResponseTo
	}

	if (idRequest == ""){
		return "",ErrRequestIdNotFound
	}

	return idRequest, nil
}

func  GetUserIdentity(r *saml.Response) (string, error){

	attrFriendlyNameIdentifier, err := config.GetString("auth:saml:idp-attribute-user-identity")
	if err != nil {
		return "", fmt.Errorf("error reading config auth:saml:idp-attribute-user-identity: %s ", err)
	}

	userIdentifier := r.GetAttribute(attrFriendlyNameIdentifier)
	if userIdentifier == ""{
	        return "", fmt.Errorf("unable to parse identity provider data - not found  <Attribute FriendlyName="+attrFriendlyNameIdentifier+">  - %s ", err) 
	}

	return userIdentifier, nil
}

func  ValidateResponse(r *saml.Response, sp *saml.ServiceProviderSettings) error {

	err := r.Validate(sp)
  	if err != nil {
    	return err
  	}

  	return nil
}