package nasdaq

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func WriteMergedJSONL[T any](path string, fresh []T, seed []T, key func(T) string, sortRecords func([]T)) ([]T, error) {
	existing := make([]T, 0)
	if len(seed) > 0 {
		existing = append(existing, seed...)
	} else {
		loaded, ok, err := ReadJSONL[T](path)
		if err != nil {
			return nil, err
		}
		if ok {
			existing = append(existing, loaded...)
		}
	}
	byKey := make(map[string]T, len(existing)+len(fresh))
	for _, record := range existing {
		recordKey := key(record)
		if recordKey == "" {
			continue
		}
		byKey[recordKey] = record
	}
	for _, record := range fresh {
		recordKey := key(record)
		if recordKey == "" {
			continue
		}
		byKey[recordKey] = record
	}
	merged := make([]T, 0, len(byKey))
	for _, record := range byKey {
		merged = append(merged, record)
	}
	if sortRecords != nil {
		sortRecords(merged)
	}
	if err := WriteJSONL(path, merged); err != nil {
		return nil, err
	}
	return merged, nil
}

func ReadJSONL[T any](path string) ([]T, bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	records := make([]T, 0)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var record T
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, true, fmt.Errorf("decode jsonl %s:%d: %w", path, lineNumber, err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, true, err
	}
	return records, true, nil
}

func WriteJSONL[T any](path string, records []T) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tempPath := path + ".tmp"
	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	writer := bufio.NewWriter(file)
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			_ = file.Close()
			_ = os.Remove(tempPath)
			return err
		}
		if _, err := writer.Write(line); err != nil {
			_ = file.Close()
			_ = os.Remove(tempPath)
			return err
		}
		if err := writer.WriteByte('\n'); err != nil {
			_ = file.Close()
			_ = os.Remove(tempPath)
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		_ = os.Remove(tempPath)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

func WriteJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}
