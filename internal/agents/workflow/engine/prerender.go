package engine

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/template"
)

// preRenderNode renders all string fields of workflow.Node before the
// executor is called, by walking the struct via reflection and reading
// json tag names. Fields whose key appears in n.ArgModes with value
// "fixed" are passed through verbatim. Default = render.
//
// Covered types:
//   - string fields    → rendered directly
//   - []string fields  → each element rendered
//   - map[string]string → each value rendered (key used for arg_modes lookup: "env.<k>")
//   - map[string]any   → top-level string values rendered; key used for arg_modes lookup
// PreRenderNode is the exported form used by callers (e.g. ExecNode)
// that bypass the engine's runOne but still need arg_modes respected.
func PreRenderNode(n workflow.Node, rctx workflow.RenderCtx) (workflow.Node, error) {
	return preRenderNode(n, rctx)
}

func preRenderNode(n workflow.Node, rctx workflow.RenderCtx) (workflow.Node, error) {
	rv := reflect.ValueOf(&n).Elem()
	rt := rv.Type()

	renderStr := func(key, val string) (string, error) {
		if n.ArgModes[key] == "fixed" || val == "" {
			return val, nil
		}
		out, err := template.Render(val, rctx)
		if err != nil {
			return "", fmt.Errorf("pre-render %s: %w", key, err)
		}
		return out, nil
	}

	for i := 0; i < rt.NumField(); i++ {
		sf := rt.Field(i)
		fv := rv.Field(i)

		jsonKey := jsonTagKey(sf)
		if jsonKey == "" {
			continue
		}

		switch sf.Type.Kind() {
		case reflect.String:
			rendered, err := renderStr(jsonKey, fv.String())
			if err != nil {
				return n, err
			}
			fv.SetString(rendered)

		case reflect.Slice:
			if sf.Type.Elem().Kind() != reflect.String {
				continue
			}
			for j := 0; j < fv.Len(); j++ {
				rendered, err := renderStr(jsonKey, fv.Index(j).String())
				if err != nil {
					return n, err
				}
				fv.Index(j).SetString(rendered)
			}

		case reflect.Map:
			if sf.Type.Key().Kind() != reflect.String {
				continue
			}
			switch sf.Type.Elem().Kind() {
			case reflect.String:
				// map[string]string — e.g. ShellEnv, Headers, Query
				for _, mk := range fv.MapKeys() {
					k := mk.String()
					modeKey := jsonKey + "." + k
					rendered, err := renderStr(modeKey, fv.MapIndex(mk).String())
					if err != nil {
						return n, err
					}
					fv.SetMapIndex(mk, reflect.ValueOf(rendered))
				}
			case reflect.Interface:
				// map[string]any — e.g. Args; render top-level string values only
				for _, mk := range fv.MapKeys() {
					k := mk.String()
					mv := fv.MapIndex(mk)
					if mv.Kind() == reflect.Interface {
						mv = mv.Elem()
					}
					if !mv.IsValid() || mv.Kind() != reflect.String {
						continue
					}
					rendered, err := renderStr(k, mv.String())
					if err != nil {
						return n, err
					}
					fv.SetMapIndex(mk, reflect.ValueOf(rendered))
				}
			}
		}
	}

	return n, nil
}

func jsonTagKey(sf reflect.StructField) string {
	tag := sf.Tag.Get("json")
	if tag == "" || tag == "-" {
		return ""
	}
	return strings.SplitN(tag, ",", 2)[0]
}
