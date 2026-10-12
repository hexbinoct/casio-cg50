package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// store keeps the app's files in the user's config folder:
//
//	settings.json            last dump, speed
//	sessions/<hash>.state    where each dump was left (resumed on the next start)
//	sessions/<hash>.slotN    quick save slots
//
// <hash> identifies the flash dump, so states never get mixed between dumps.
type store struct{ dir string }

type settings struct {
	LastDump string `json:"lastDump"`
	Speed    string `json:"speed"`
}

type dumpInfo struct {
	Path string
	Name string
	Hash string // first 16 hex digits of the SHA-256 of the file
	Size int
}

func newStore() *store {
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	s := &store{dir: filepath.Join(base, appName)}
	os.MkdirAll(filepath.Join(s.dir, "sessions"), 0o755)
	return s
}

func (s *store) settings() settings {
	st := settings{Speed: "1x"}
	if b, err := os.ReadFile(filepath.Join(s.dir, "settings.json")); err == nil {
		json.Unmarshal(b, &st)
	}
	if _, ok := speeds[st.Speed]; !ok {
		st.Speed = "1x"
	}
	return st
}

func (s *store) saveSettings(st settings) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	return os.WriteFile(filepath.Join(s.dir, "settings.json"), b, 0o644)
}

func (s *store) sessionPath(hash, kind string) string {
	return filepath.Join(s.dir, "sessions", hash+"."+kind)
}

func (s *store) read(hash, kind string) ([]byte, error) { return os.ReadFile(s.sessionPath(hash, kind)) }

func (s *store) write(hash, kind string, b []byte) error {
	p := s.sessionPath(hash, kind)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (s *store) remove(hash, kind string) { os.Remove(s.sessionPath(hash, kind)) }

// readDump loads and checks a flash dump file.
func readDump(path string) ([]byte, dumpInfo, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, dumpInfo{}, err
	}
	if len(b) != 16<<20 && len(b) != 32<<20 {
		return nil, dumpInfo{}, fmt.Errorf("%s is %d bytes: a flash dump is 16 MB or 32 MB",
			filepath.Base(path), len(b))
	}
	if !bytes.Contains(b[:0x1000], []byte("CASIO")) {
		// the boot code's header carries "CASIO" in its first KB (offset 824 in the 3.60 dump)
		return nil, dumpInfo{}, errors.New("this file doesn't look like an fx-CG50 flash dump")
	}
	h := sha256.Sum256(b)
	return b, dumpInfo{Path: path, Name: filepath.Base(path), Hash: hex.EncodeToString(h[:8]), Size: len(b)}, nil
}

// A state file saved with "Save state as…": a header naming the dump, then the core's blob.
const stateFileMagic = "CG50STUDIO-STATE 1\n"

func wrapState(hash string, blob []byte) []byte {
	return append([]byte(stateFileMagic+hash+"\n"), blob...)
}

func unwrapState(b []byte, hash string) ([]byte, error) {
	if !bytes.HasPrefix(b, []byte(stateFileMagic)) {
		return nil, errors.New("not a CG50 Studio state file")
	}
	rest := b[len(stateFileMagic):]
	nl := bytes.IndexByte(rest, '\n')
	if nl < 0 {
		return nil, errors.New("damaged state file")
	}
	if string(rest[:nl]) != hash {
		return nil, errors.New("this state was saved with a different flash dump")
	}
	return rest[nl+1:], nil
}
