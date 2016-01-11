package cloudconfig // import "github.com/cafebazaar/blacksmith/cloudconfig"

import (
	"bytes"
	"encoding/base64"
	"io/ioutil"
	"path"
	"strings"
	"sync"
	"text/template"

	"github.com/cafebazaar/blacksmith/logging"
)

type Repo struct {
	templates   *template.Template
	dataSources map[string]DataSource
	executeLock sync.Mutex
}

func findFiles(path string) ([]string, error) {
	infos, err := ioutil.ReadDir(path)
	if err != nil {
		return nil, err
	}

	files := make([]string, 0)
	for i := range infos {
		if !infos[i].IsDir() && infos[i].Name()[0] != '.' {
			files = append(files, infos[i].Name())
		}
	}
	return files, nil
}

func FromPath(dataSources map[string]DataSource, tmplPath string) (*Repo, error) {
	//	logging.Log("REPO", "In FromPath")
	files, err := findFiles(tmplPath)
	if err != nil {
		return nil, err
	}
	var therepos string
	t := template.New("")
	t.Delims("<<", ">>")
	t.Funcs(map[string]interface{}{
		"V": func(key string) (interface{}, error) {
			return "FUNC PLACEHOLDER", nil
		},
		"S": func(key string, value string) (interface{}, error) {
			return "FUNC PLACEHOLDER", nil
		},
		"D": func(key string) (interface{}, error) {
			return "FUNC PLACEHOLDER", nil
		},
		"VD": func(key string) (interface{}, error) {
			return "FUNC PLACEHOLDER", nil
		},
		"b64": func(text string) interface{} {
			return "FUNC PLACEHOLDER"
		},
		"b64template": func(text string) (interface{}, error) {
			return "FUNC PLACEHOLDER", nil
		},
	})

	for i := range files {
		files[i] = path.Join(tmplPath, files[i])
	}

	t, err = t.ParseFiles(files...)
	if err != nil {
		return nil, err
	}

	//	logging.Log("REPO", "when assigning")
	for k, _ := range dataSources {
		therepos += k + " "
	}
	//	logging.Log("", "")
	logging.Log("REPO", therepos)
	return &Repo{
		templates:   t,
		dataSources: dataSources,
	}, nil
}

func (r *Repo) ExecuteTemplate(templateName string, c *ConfigContext) (string, error) {
	// rewrite funcs to include context and hold a lock so it doesn't get overwrite
	buf := new(bytes.Buffer)
	r.templates.Funcs(map[string]interface{}{
		"V": func(key string) (interface{}, error) {
			return GetValue(r.dataSources, c, key)
		},
		"S": func(key string, value string) (interface{}, error) {
			return Set(r.dataSources, c, key, value)
		},
		"VD": func(key string) (interface{}, error) {
			return GetAndDelete(r.dataSources, c, key)
		},
		"D": func(key string) (interface{}, error) {
			return Delete(r.dataSources, c, key)
		},
		"b64": func(text string) interface{} {
			return base64.StdEncoding.EncodeToString([]byte(text))
		},
		"b64template": func(templateName string) (interface{}, error) {
			text, err := r.ExecuteTemplate(templateName, c)
			if err != nil {
				return nil, err
			}
			return base64.StdEncoding.EncodeToString([]byte(text)), nil
		},
	})
	err := r.templates.ExecuteTemplate(buf, templateName, nil)
	if err != nil {
		return "", err
	}
	str := buf.String()
	str = strings.Trim(str, "\n")
	return str, err
}

func (r *Repo) GenerateConfig(c *ConfigContext) (string, error) {
	r.executeLock.Lock()
	defer r.executeLock.Unlock()

	if r.templates.Lookup("main") == nil {
		return "", nil
	}
	return r.ExecuteTemplate("main", c)
}
