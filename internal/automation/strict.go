package automation

import (
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// unknownFields reports the first mapping key in node that the struct
// type t (or the structs nested in it) has no yaml tag for. The decoder's
// own KnownFields check does not reach inside custom unmarshalers, so the
// document types check themselves.
func unknownFields(node *yaml.Node, t reflect.Type) error {
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		node = node.Content[0]
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t.Kind() == reflect.Slice && node.Kind == yaml.SequenceNode:
		for _, item := range node.Content {
			if err := unknownFields(item, t.Elem()); err != nil {
				return err
			}
		}
	case t.Kind() == reflect.Struct && node.Kind == yaml.MappingNode:
		fields := make(map[string]reflect.Type, t.NumField())
		for i := range t.NumField() {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			if name == "" {
				name = strings.ToLower(f.Name)
			}
			if name != "-" {
				fields[name] = f.Type
			}
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			ft, ok := fields[key.Value]
			if !ok {
				return fmt.Errorf("line %d: unknown field %q", key.Line, key.Value)
			}
			if err := unknownFields(value, ft); err != nil {
				return err
			}
		}
	}
	return nil
}
