package contract

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

const maxContractBytes int64 = 256 << 10

// CanonicalContractSHA256 is the SHA-256 digest of the semantic, canonical
// JSON representation of the complete v1 framework contract compiled into
// this CLI. Whitespace and object-key ordering do not affect the digest.
const CanonicalContractSHA256 = "145c0ab77e286199bdb738d32fe619e0aef4c828bbc766c03f9c97627ec92c91"

//go:embed canonical.json
var canonicalContractJSON []byte

// CanonicalJSON returns a copy of the complete framework contract supported by
// this CLI release. It is primarily useful to create exact compatibility
// fixtures; callers must not treat it as a customizable workspace contract.
func CanonicalJSON() []byte {
	return bytes.Clone(canonicalContractJSON)
}

func validateCanonicalContract(data []byte) error {
	expected, err := semanticJSONDigest(canonicalContractJSON)
	if err != nil {
		return fmt.Errorf("compiled canonical framework contract is invalid: %w", err)
	}
	if hex.EncodeToString(expected[:]) != CanonicalContractSHA256 {
		return errors.New("compiled canonical framework contract digest does not match CanonicalContractSHA256")
	}
	actual, err := semanticJSONDigest(data)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf(
			"framework contract does not match the complete canonical contract for this CLI release (expected sha256:%s, got sha256:%s)",
			CanonicalContractSHA256,
			hex.EncodeToString(actual[:]),
		)
	}
	return nil
}

func semanticJSONDigest(data []byte) ([sha256.Size]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	value, err := decodeUniqueJSONValue(decoder)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("parse framework contract: %w", err)
	}
	if token, trailingErr := decoder.Token(); !errors.Is(trailingErr, io.EOF) {
		if trailingErr == nil {
			return [sha256.Size]byte{}, fmt.Errorf("parse framework contract: unexpected trailing token %v", token)
		}
		return [sha256.Size]byte{}, fmt.Errorf("parse framework contract: %w", trailingErr)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("canonicalize framework contract: %w", err)
	}
	return sha256.Sum256(canonical), nil
}

// SemanticJSONSHA256 returns the SHA-256 of a canonical JSON representation
// while rejecting duplicate keys and trailing content. Framework artifacts use
// it to authenticate semantics without making whitespace significant.
func SemanticJSONSHA256(data []byte) (string, error) {
	digest, err := semanticJSONDigest(data)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(digest[:]), nil
}

func decodeUniqueJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, keyErr := decoder.Token()
			if keyErr != nil {
				return nil, keyErr
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("object key is not a string: %v", keyToken)
			}
			if _, duplicate := object[key]; duplicate {
				return nil, fmt.Errorf("duplicate object key %q", key)
			}
			value, valueErr := decodeUniqueJSONValue(decoder)
			if valueErr != nil {
				return nil, valueErr
			}
			object[key] = value
		}
		end, endErr := decoder.Token()
		if endErr != nil {
			return nil, endErr
		}
		if end != json.Delim('}') {
			return nil, fmt.Errorf("unexpected object terminator %v", end)
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, valueErr := decodeUniqueJSONValue(decoder)
			if valueErr != nil {
				return nil, valueErr
			}
			array = append(array, value)
		}
		end, endErr := decoder.Token()
		if endErr != nil {
			return nil, endErr
		}
		if end != json.Delim(']') {
			return nil, fmt.Errorf("unexpected array terminator %v", end)
		}
		return array, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

func readContractFile(workspaceRoot string) ([]byte, error) {
	return readBoundedWorkspaceRegularFile(workspaceRoot, fileName, 1, maxContractBytes)
}

func readBoundedWorkspaceRegularFile(workspaceRoot, name string, minBytes, maxBytes int64) ([]byte, error) {
	if minBytes < 0 || maxBytes < minBytes {
		return nil, fmt.Errorf("invalid bounded workspace-file size range")
	}
	root, err := os.OpenRoot(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("open workspace root: %w", err)
	}
	defer root.Close()

	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("workspace file must not be a symbolic link: %s", name)
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("workspace file must be a regular file: %s", name)
	}
	if before.Size() < minBytes || before.Size() > maxBytes {
		return nil, fmt.Errorf("workspace file size must be between %d and %d bytes: %s", minBytes, maxBytes, name)
	}

	file, err := openWorkspaceRegularFile(root, name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("workspace file identity changed while opening: %s", name)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("workspace file exceeds %d bytes: %s", maxBytes, name)
	}

	afterOpen, err := file.Stat()
	if err != nil {
		return nil, err
	}
	afterPath, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("reinspect workspace file %s: %w", name, err)
	}
	if afterPath.Mode()&os.ModeSymlink != 0 || !afterPath.Mode().IsRegular() ||
		!os.SameFile(before, afterPath) || !os.SameFile(opened, afterOpen) ||
		before.Size() != opened.Size() || opened.Size() != afterOpen.Size() ||
		afterOpen.Size() != afterPath.Size() || before.Size() != int64(len(data)) ||
		before.Mode() != opened.Mode() || opened.Mode() != afterOpen.Mode() ||
		afterOpen.Mode() != afterPath.Mode() ||
		!before.ModTime().Equal(opened.ModTime()) || !opened.ModTime().Equal(afterOpen.ModTime()) ||
		!afterOpen.ModTime().Equal(afterPath.ModTime()) {
		return nil, fmt.Errorf("workspace file changed while it was being read: %s", name)
	}
	return data, nil
}
