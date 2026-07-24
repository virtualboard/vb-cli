// Package frameworkschema authenticates the fixed JSON Schemas compiled into
// this CLI. Workspace copies are integrity mirrors, never executable schema
// configuration or remote-reference entry points.
package frameworkschema

import (
	"bytes"
	_ "embed"
	"fmt"

	"github.com/virtualboard/vb-cli/internal/contract"
	"github.com/virtualboard/vb-cli/internal/util"
)

const maxSchemaBytes int64 = 256 << 10

const (
	FeatureSHA256    = "bae8d4d3a128f4f5d3fdb08d74275f2c4e83e3e519a0d31cd2186411881a0287"
	SystemSpecSHA256 = "d3d1da42ab94d58b2ff2ced0e3004db2787758ef03e257bdbc9e2670c76a9e89"
)

//go:embed frontmatter.schema.json
var featureSchema []byte

//go:embed system-spec.schema.json
var systemSpecSchema []byte

func Feature(workspaceRoot, workspacePath string) ([]byte, error) {
	return verify(workspaceRoot, workspacePath, featureSchema, FeatureSHA256, "feature")
}

func SystemSpec(workspaceRoot, workspacePath string) ([]byte, error) {
	return verify(workspaceRoot, workspacePath, systemSpecSchema, SystemSpecSHA256, "system-spec")
}

// CanonicalFeature returns a copy for trusted fixture/bootstrap generation.
func CanonicalFeature() []byte { return bytes.Clone(featureSchema) }

// CanonicalSystemSpec returns a copy for trusted fixture/bootstrap generation.
func CanonicalSystemSpec() []byte { return bytes.Clone(systemSpecSchema) }

func verify(workspaceRoot, workspacePath string, embedded []byte, expectedDigest, label string) ([]byte, error) {
	compiledDigest, err := contract.SemanticJSONSHA256(embedded)
	if err != nil || compiledDigest != expectedDigest {
		return nil, fmt.Errorf("compiled %s schema integrity failure", label)
	}
	workspaceData, _, err := util.ReadRegularFileWithin(workspaceRoot, workspacePath, maxSchemaBytes)
	if err != nil {
		return nil, fmt.Errorf("read canonical %s schema: %w", label, err)
	}
	workspaceDigest, err := contract.SemanticJSONSHA256(workspaceData)
	if err != nil {
		return nil, fmt.Errorf("parse canonical %s schema: %w", label, err)
	}
	if workspaceDigest != expectedDigest {
		return nil, fmt.Errorf("workspace %s schema does not match the fixed CLI contract (expected sha256:%s, got sha256:%s)", label, expectedDigest, workspaceDigest)
	}
	return bytes.Clone(embedded), nil
}
