/*
Copyright 2022 Red Hat

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	corev1 "k8s.io/api/core/v1"
)

// TType - TemplateType
type TType string

const (
	// TemplateTypeScripts - scripts type
	TemplateTypeScripts TType = "bin"
	// TemplateTypeConfig - config type
	TemplateTypeConfig TType = "config"
	// TemplateTypeCustom - custom config type, the secret/cm will not get upated as it is exected that the content is owned by a user
	// if the configmap/secret does not exist on first check, it gets created
	TemplateTypeCustom TType = "custom"
	// TemplateTypeNone - none type, don't add configs from a directory, only files from AdditionalData
	TemplateTypeNone TType = "none"
)

// Template - config map and secret details
type Template struct {
	Name               string                 // name of the cm/secret to create based of the Template. Check secret/configmap pkg on details how it is used.
	Namespace          string                 // name of the nanmespace to create the cm/secret. Check secret/configmap pkg on details how it is used.
	Type               TType                  // type of the templates, see TTtypes
	InstanceType       string                 // the CRD name in lower case, to separate the templates for each CRD in /templates
	SecretType         corev1.SecretType      // Secrets only, defaults to "Opaque"
	AdditionalTemplate map[string]string      // templates which are common to multiple CRDs can be located in a shared folder and added via this type into the resulting CM/secret
	CustomData         map[string]string      // custom data which won't get rendered as a template and just added to the resulting cm/secret
	Labels             map[string]string      // labels to be set on the cm/secret
	Annotations        map[string]string      // Annotations set on cm/secret
	ConfigOptions      map[string]interface{} // map of parameters as input data to render the templates
	SkipSetOwner       bool                   // skip setting ownership on the associated configmap
	Version            string                 // optional version string to separate templates inside the InstanceType/Type directory. E.g. placementapi/config/18.0
}

// GetTemplatesPath get path to templates, either running local or deployed as container
func GetTemplatesPath() string {

	templates := os.Getenv("OPERATOR_TEMPLATES")
	templatesPath := ""
	if templates == "" {
		// support local testing with 'up local'
		_, basefile, _, _ := runtime.Caller(1)
		templatesPath = path.Join(path.Dir(basefile), "../../templates")
	} else {
		// deployed as a container
		templatesPath = templates
	}

	return templatesPath
}

//
// GetAllTemplates - get all template files
//
// The structur of the folder is, base path, the kind (CRD in lower case),
// - path - base path of the templates folder
// - kind - sub folder for the CRDs templates
// - templateType - TType of the templates. When the templates got rendered and added to a CM
//   this information is e.g. used for the permissions they get mounted into the pod
// - version - if there need to be templates for different versions, they can be stored in a version subdir
//
// Sub directories inside the specified directory with the above parameters get ignored.
func GetAllTemplates(path string, kind string, templateType string, version string) []string {

	templatePath := filepath.Join(path, strings.ToLower(kind), templateType, "*")

	if version != "" {
		templatePath = filepath.Join(path, strings.ToLower(kind), templateType, version, "*")
	}

	templatesFiles, err := filepath.Glob(templatePath)
	if err != nil {
		fmt.Print(err)
		os.Exit(1)
	}

	// remove any subdiretories from templatesFiles
	for index := 0; index < len(templatesFiles); index++ {
		fi, err := os.Stat(templatesFiles[index])
		if err != nil {
			fmt.Print(err)
			os.Exit(1)
		}
		if fi.Mode().IsDir() {
			templatesFiles = RemoveIndex(templatesFiles, index)
			index = -1 // restart from the beginning
		}
	}

	return templatesFiles
}

// ExecuteTemplate creates a template from the file and
// execute it with the specified data
func ExecuteTemplate(templateFile string, data interface{}) (string, error) {

	b, err := ioutil.ReadFile(templateFile)
	if err != nil {
		return "", err
	}
	file := string(b)

	renderedTemplate, err := ExecuteTemplateData(file, data)
	if err != nil {
		return "", err
	}
	return renderedTemplate, nil
}

// template function to increment an int
func add(x, y int) int {
	return x + y
}

// template function to lower a string
func lower(s string) string {
	return strings.ToLower(s)
}

// ExecuteTemplateData creates a template from string and
// execute it with the specified data
func ExecuteTemplateData(templateData string, data interface{}) (string, error) {

	var buff bytes.Buffer
	funcs := template.FuncMap{
		"add":   add,
		"lower": lower,
	}
	tmpl, err := template.New("tmp").Funcs(funcs).Parse(templateData)
	if err != nil {
		return "", err
	}
	err = tmpl.Execute(&buff, data)
	if err != nil {
		return "", err
	}
	return buff.String(), nil
}

// ExecuteTemplateFile - creates a template from the file and
// execute it with the specified data
func ExecuteTemplateFile(filename string, data interface{}) (string, error) {

	templates := os.Getenv("OPERATOR_TEMPLATES")
	filepath := ""
	if templates == "" {
		// support local testing with 'up local'
		_, basefile, _, _ := runtime.Caller(1)
		filepath = path.Join(path.Dir(basefile), "../../templates/"+filename)
	} else {
		// deployed as a container
		filepath = path.Join(templates + filename)
	}

	b, err := ioutil.ReadFile(filepath)
	if err != nil {
		return "", err
	}
	file := string(b)
	var buff bytes.Buffer
	funcs := template.FuncMap{
		"add":   add,
		"lower": lower,
	}
	tmpl, err := template.New("tmp").Funcs(funcs).Parse(file)
	if err != nil {
		return "", err
	}
	err = tmpl.Execute(&buff, data)
	if err != nil {
		return "", err
	}
	return buff.String(), nil
}

// GetTemplateData - Renders templates specified via Template struct
//
// Check the TType const and Template type for more details on defining the template.
func GetTemplateData(t Template) (map[string]string, error) {
	opts := t.ConfigOptions

	// get templates base path, either running local or deployed as container
	templatesPath := GetTemplatesPath()

	data := make(map[string]string)

	if t.Type != TemplateTypeNone {
		// get all scripts templates which are in ../templesPath/cr.Kind/CMType/<OSPVersion - optional>
		templatesFiles := GetAllTemplates(templatesPath, t.InstanceType, string(t.Type), string(t.Version))

		// render all template files
		for _, file := range templatesFiles {
			renderedData, err := ExecuteTemplate(file, opts)
			if err != nil {
				return data, err
			}
			data[filepath.Base(file)] = renderedData
		}
	}
	// add additional template files from different directory, which
	// e.g. can be common to multiple controllers
	for filename, file := range t.AdditionalTemplate {
		renderedTemplate, err := ExecuteTemplateFile(file, opts)
		if err != nil {
			return nil, err
		}
		data[filename] = renderedTemplate
	}

	return data, nil
}
