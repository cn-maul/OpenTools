package hostservice

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// secretDocument is the on-disk shape of one tool's secrets. It is always
// encrypted before it reaches the disk.
type secretDocument map[string]string

func (s *Service) secretPath(toolID string) (string, error) {
	dir, err := s.toolDir(toolID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "secrets.bin"), nil
}

func (s *Service) loadSecrets(toolID string) (secretDocument, error) {
	path, err := s.secretPath(toolID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return secretDocument{}, nil
	}
	if err != nil {
		return nil, err
	}
	plain, err := unprotect(data)
	if err != nil {
		// A file written by another machine or user cannot be decrypted.
		// Treat it as empty rather than bricking the tool.
		return secretDocument{}, nil
	}
	var document secretDocument
	if err := json.Unmarshal(plain, &document); err != nil {
		return secretDocument{}, nil
	}
	if document == nil {
		document = secretDocument{}
	}
	return document, nil
}

func (s *Service) saveSecrets(toolID string, document secretDocument) error {
	path, err := s.secretPath(toolID)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(document)
	if err != nil {
		return err
	}
	sealed, err := protect(plain)
	if err != nil {
		return fmt.Errorf("encrypt secrets: %w", err)
	}
	return writeAtomic(path, sealed, 0o600)
}

func (s *Service) readSecret(toolID, name string) (string, error) {
	name = normalizeSecretName(name)
	if name == "" {
		return "", errors.New("secret name is empty")
	}
	document, err := s.loadSecrets(toolID)
	if err != nil {
		return "", err
	}
	return document[name], nil
}

func (s *Service) writeSecret(toolID, name, value string) error {
	name = normalizeSecretName(name)
	if name == "" {
		return errors.New("secret name is empty")
	}
	document, err := s.loadSecrets(toolID)
	if err != nil {
		return err
	}
	if value == "" {
		delete(document, name)
	} else {
		document[name] = value
	}
	return s.saveSecrets(toolID, document)
}

func (s *Service) deleteSecret(toolID, name string) error {
	return s.writeSecret(toolID, name, "")
}

func normalizeSecretName(name string) string {
	const limit = 64
	out := make([]rune, 0, len(name))
	for _, char := range name {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9':
			out = append(out, char)
		case char == '.' || char == '-' || char == '_' || char == ':':
			out = append(out, char)
		default:
			continue
		}
		if len(out) >= limit {
			break
		}
	}
	if len(out) == 0 {
		return ""
	}
	return string(out)
}
