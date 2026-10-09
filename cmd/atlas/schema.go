package main

import (
	"fmt"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// Schema indexes the fields of an SDL document by "Type.field".
type Schema struct {
	Fields map[string]*ast.FieldDefinition
}

func ParseSchema(sdl string) (*Schema, error) {
	doc, err := parser.ParseSchema(&ast.Source{Name: "schema.graphqls", Input: sdl})
	if err != nil {
		return nil, err
	}
	s := &Schema{Fields: map[string]*ast.FieldDefinition{}}
	add := func(defs ast.DefinitionList) {
		for _, d := range defs {
			for _, f := range d.Fields {
				s.Fields[d.Name+"."+f.Name] = f
			}
		}
	}
	add(doc.Definitions)
	add(doc.Extensions)
	return s, nil
}

var keptDirectives = map[string]bool{"deprecated": true, "experimental": true}

func directivesSuffix(ds ast.DirectiveList) string {
	var out []string
	for _, d := range ds {
		if keptDirectives[d.Name] {
			out = append(out, "@"+d.Name)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return "  " + strings.Join(out, " ")
}

// Signature renders a field the way the browser shows it: one line when it
// fits, otherwise one argument per line.
func Signature(f *ast.FieldDefinition) []string {
	ret := f.Type.String()
	fd := directivesSuffix(f.Directives)
	if len(f.Arguments) == 0 {
		return []string{fmt.Sprintf("%s: %s%s", f.Name, ret, fd)}
	}
	var args []string
	for _, a := range f.Arguments {
		s := a.Name + ": " + a.Type.String()
		if a.DefaultValue != nil {
			s += " = " + a.DefaultValue.String()
		}
		s += directivesSuffix(a.Directives)
		// Object arguments are IDs in the SDL; show the type they expect.
		if d := a.Directives.ForName("expectedType"); d != nil {
			if arg := d.Arguments.ForName("name"); arg != nil && arg.Value != nil {
				s += "  # " + arg.Value.Raw
			}
		}
		args = append(args, s)
	}
	one := fmt.Sprintf("%s(%s): %s%s", f.Name, strings.Join(args, ", "), ret, fd)
	if len(one) <= 88 && !strings.Contains(one, "  @") && !strings.Contains(one, "  #") {
		return []string{one}
	}
	lines := []string{f.Name + "("}
	for _, a := range args {
		lines = append(lines, "  "+a)
	}
	return append(lines, "): "+ret+fd)
}
