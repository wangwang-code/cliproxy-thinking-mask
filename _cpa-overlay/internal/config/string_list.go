package config

import "gopkg.in/yaml.v3"

// StringList is a []string that marshals every element as a double-quoted YAML
// scalar. This avoids yaml.v3's block-scalar encoding for strings with leading
// newlines, which is not round-trippable through the comment-preserving saver.
type StringList []string

// MarshalYAML emits a YAML sequence whose items are always double-quoted so
// values containing newlines (for example fake-thinking texts) survive
// SaveConfigPreserveComments without triggering yaml.v3's block-scalar encoder.
func (s StringList) MarshalYAML() (interface{}, error) {
	node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, item := range s {
		node.Content = append(node.Content, &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!str",
			Value: item,
			Style: yaml.DoubleQuotedStyle,
		})
	}
	return node, nil
}
