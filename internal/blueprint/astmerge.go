package blueprint

import (
	"bytes"
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// MergeYAMLNode applies an RFC 7396 merge-patch map onto an existing YAML document
// at the yaml.Node AST level, preserving comments and key ordering on untouched keys.
func MergeYAMLNode(existing []byte, patch map[string]any) ([]byte, error) {
	if len(patch) == 0 {
		return existing, nil
	}
	var doc yaml.Node
	if len(bytes.TrimSpace(existing)) == 0 {
		doc = yaml.Node{
			Kind:    yaml.DocumentNode,
			Content: []*yaml.Node{{Kind: yaml.MappingNode}},
		}
	} else {
		if err := yaml.Unmarshal(existing, &doc); err != nil {
			return nil, err
		}
		if len(doc.Content) == 0 {
			doc.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
		}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("YAML root must be a mapping")
	}
	if err := mergeMappingNode(root, patch); err != nil {
		return nil, err
	}
	return encodeNode(&doc)
}

func mergeMappingNode(dst *yaml.Node, patch map[string]any) error {
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		val := patch[k]
		idx := findMappingKey(dst, k)
		if val == nil {
			if idx >= 0 {
				dst.Content = append(dst.Content[:idx], dst.Content[idx+2:]...)
			}
			continue
		}
		if subMap, ok := val.(map[string]any); ok {
			if idx >= 0 && dst.Content[idx+1].Kind == yaml.MappingNode {
				if err := mergeMappingNode(dst.Content[idx+1], subMap); err != nil {
					return err
				}
				continue
			}
		}
		valNode, err := toYAMLNode(val)
		if err != nil {
			return err
		}
		if idx >= 0 {
			// Preserve key comments and line comment on the replaced value where possible.
			valNode.LineComment = dst.Content[idx+1].LineComment
			dst.Content[idx+1] = valNode
		} else {
			keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}
			dst.Content = append(dst.Content, keyNode, valNode)
		}
	}
	return nil
}

// AppendYAMLSequence appends unique string entries to a top-level YAML sequence
// key (such as services: in plat5.yml or include: in docker-compose.yml),
// preserving existing comments and structure.
func AppendYAMLSequence(existing []byte, key string, items []string) ([]byte, error) {
	if len(items) == 0 {
		return existing, nil
	}
	var doc yaml.Node
	if len(bytes.TrimSpace(existing)) == 0 {
		doc = yaml.Node{
			Kind:    yaml.DocumentNode,
			Content: []*yaml.Node{{Kind: yaml.MappingNode}},
		}
	} else {
		if err := yaml.Unmarshal(existing, &doc); err != nil {
			return nil, err
		}
		if len(doc.Content) == 0 {
			doc.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
		}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("YAML root must be a mapping")
	}
	idx := findMappingKey(root, key)
	var seq *yaml.Node
	if idx < 0 {
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
		seq = &yaml.Node{Kind: yaml.SequenceNode}
		root.Content = append(root.Content, keyNode, seq)
	} else {
		seq = root.Content[idx+1]
		if seq.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("%s must be a YAML sequence", key)
		}
	}
	seen := map[string]bool{}
	for _, n := range seq.Content {
		seen[n.Value] = true
	}
	for _, item := range items {
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: item})
	}
	return encodeNode(&doc)
}

func findMappingKey(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

func toYAMLNode(v any) (*yaml.Node, error) {
	var doc yaml.Node
	raw, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, nil
	}
	return doc.Content[0], nil
}

func encodeNode(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
