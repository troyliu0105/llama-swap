package proxy

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/mostlygeek/llama-swap/internal/logmon"
)

const (
	captureStoreMagic      = "CSWP"
	captureStoreHeaderSize = 8
	captureStoreEntrySize  = 8
)

// captureEntry stores one compressed capture loaded from disk.
type captureEntry struct {
	ID   int
	Data []byte
}

// captureStore handles file-based persistence of compressed capture data.
type captureStore struct {
	path       string
	maxEntries int
	mu         sync.Mutex
	logger     *logmon.Monitor
}

func newCaptureStore(path string, maxEntries int, logger *logmon.Monitor) *captureStore {
	if maxEntries <= 0 {
		maxEntries = 5120
	}
	return &captureStore{
		path:       path,
		maxEntries: maxEntries,
		logger:     logger,
	}
}

// Append writes a single capture entry to the persistence file.
// If the file doesn't exist, creates it with header.
// Thread-safe.
func (s *captureStore) Append(id int, compressedData []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id < 0 {
		return fmt.Errorf("capture id must be non-negative: %d", id)
	}
	if len(compressedData) > int(^uint32(0)) {
		return fmt.Errorf("capture data too large: %d bytes", len(compressedData))
	}

	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	count, err := s.readHeader(file)
	if err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	if err := writeCaptureEntry(file, captureEntry{ID: id, Data: compressedData}); err != nil {
		return err
	}
	if _, err := file.Seek(int64(len(captureStoreMagic)), io.SeekStart); err != nil {
		return err
	}
	return binary.Write(file, binary.BigEndian, count+1)
}

// Load reads all entries from the persistence file.
// Returns ordered slice of (id, compressedData) pairs.
// If file doesn't exist, returns empty slice.
func (s *captureStore) Load() ([]captureEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.loadLocked()
}

// Compact rewrites the file keeping only the last maxEntries entries.
// Called on startup if needed.
func (s *captureStore) Compact() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.loadLocked()
	if err != nil {
		return err
	}
	if len(entries) > s.maxEntries {
		entries = entries[len(entries)-s.maxEntries:]
	}
	return s.rewriteLocked(entries)
}

func (s *captureStore) loadLocked() ([]captureEntry, error) {
	file, err := os.OpenFile(s.path, os.O_RDWR, 0o600)
	if os.IsNotExist(err) {
		return []captureEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	count, err := s.readHeader(file)
	if err != nil {
		return nil, err
	}

	entries := make([]captureEntry, 0, count)
	for i := uint32(0); i < count; i++ {
		entry, err := readCaptureEntry(file)
		if err != nil {
			return nil, fmt.Errorf("read capture entry %d: %w", i, err)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (s *captureStore) readHeader(file *os.File) (uint32, error) {
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if info.Size() == 0 {
		if err := writeCaptureHeader(file, 0); err != nil {
			return 0, err
		}
		return 0, nil
	}
	if info.Size() < captureStoreHeaderSize {
		return 0, fmt.Errorf("capture store header too short: %d bytes", info.Size())
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	magic := make([]byte, len(captureStoreMagic))
	if _, err := io.ReadFull(file, magic); err != nil {
		return 0, err
	}
	if string(magic) != captureStoreMagic {
		return 0, fmt.Errorf("invalid capture store magic %q", string(magic))
	}
	var count uint32
	if err := binary.Read(file, binary.BigEndian, &count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *captureStore) rewriteLocked(entries []captureEntry) error {
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	if len(entries) > int(^uint32(0)) {
		return fmt.Errorf("too many capture entries: %d", len(entries))
	}
	if err := writeCaptureHeader(file, uint32(len(entries))); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := writeCaptureEntry(file, entry); err != nil {
			return err
		}
	}
	return nil
}

func writeCaptureHeader(w io.Writer, count uint32) error {
	if _, err := w.Write([]byte(captureStoreMagic)); err != nil {
		return err
	}
	return binary.Write(w, binary.BigEndian, count)
}

func writeCaptureEntry(w io.Writer, entry captureEntry) error {
	if entry.ID < 0 {
		return fmt.Errorf("capture id must be non-negative: %d", entry.ID)
	}
	if len(entry.Data) > int(^uint32(0)) {
		return fmt.Errorf("capture data too large: %d bytes", len(entry.Data))
	}
	if err := binary.Write(w, binary.BigEndian, uint32(entry.ID)); err != nil {
		return err
	}
	if err := binary.Write(w, binary.BigEndian, uint32(len(entry.Data))); err != nil {
		return err
	}
	_, err := w.Write(entry.Data)
	return err
}

func readCaptureEntry(r io.Reader) (captureEntry, error) {
	var id uint32
	if err := binary.Read(r, binary.BigEndian, &id); err != nil {
		return captureEntry{}, err
	}
	var dataLen uint32
	if err := binary.Read(r, binary.BigEndian, &dataLen); err != nil {
		return captureEntry{}, err
	}
	data := make([]byte, dataLen)
	if _, err := io.ReadFull(r, data); err != nil {
		return captureEntry{}, err
	}
	return captureEntry{ID: int(id), Data: data}, nil
}
