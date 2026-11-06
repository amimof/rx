// Package provider runs the exec providers declared in YAML plugins.
package provider

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"text/template"

	"github.com/amimof/rx/internal/complete"
	"github.com/amimof/rx/internal/model"
	"github.com/amimof/rx/internal/parse"
	"github.com/amimof/rx/internal/spec"
)

var funcs = template.FuncMap{
	"default": func(def string, v any) string {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
		return def
	},
	"quote": parse.Quote,
}

// Render expands the provider's exec template with the command's slot
// values, keyed by slot key (e.g. {{.namespace}}). Missing keys render as
// empty strings.
func Render(p spec.Provider, cmd *model.Command) (string, error) {
	t, err := template.New("exec").Option("missingkey=zero").Funcs(funcs).Parse(p.Exec)
	if err != nil {
		return "", err
	}
	data := map[string]any{}
	for _, s := range cmd.Slots {
		if s.Value != "" {
			data[s.Key] = s.Value
		}
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return strings.ReplaceAll(buf.String(), "<no value>", ""), nil
}

// Run executes the provider script with sh and returns one candidate per
// output line. A line may contain "value\tdescription".
func Run(ctx context.Context, name string, p spec.Provider, script string, env []string) (complete.Result, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	cmd.Env = env
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return complete.Result{}, ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if i := strings.IndexByte(msg, '\n'); i > 0 {
			msg = msg[:i]
		}
		return complete.Result{}, fmt.Errorf("provider %s: %v: %s", name, err, msg)
	}
	var res complete.Result
	sc := bufio.NewScanner(&out)
	for sc.Scan() {
		v, d, _ := strings.Cut(sc.Text(), "\t")
		v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), p.Strip))
		if v != "" {
			res.Candidates = append(res.Candidates, complete.Candidate{Value: v, Desc: d, Source: name})
		}
	}
	return res, sc.Err()
}
