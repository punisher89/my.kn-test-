package pilot

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

const conntrackSysctlDir = "/proc/sys/net/netfilter"

// Firmware may compile accounting in without exposing the Linux sysctl knob.
// ENOENT means there is no tunable, not that connbytes should be removed.
func readOptionalSysctl(path string) ([]byte, error) {
	value, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(value)
	if !bytes.Equal(trimmed, []byte("0")) && !bytes.Equal(trimmed, []byte("1")) {
		return nil, fmt.Errorf("неожиданное значение %s", filepath.Base(path))
	}
	return value, nil
}

func readConntrackSettings(dir string) (liberal, acct []byte, err error) {
	liberal, err = readOptionalSysctl(filepath.Join(dir, "nf_conntrack_tcp_be_liberal"))
	if err != nil {
		return nil, nil, err
	}
	acct, err = readOptionalSysctl(filepath.Join(dir, "nf_conntrack_acct"))
	if err != nil {
		return nil, nil, err
	}
	return liberal, acct, nil
}

func writeExistingSysctl(path string, value []byte) error {
	// Never synthesize a missing kernel knob as an ordinary file.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	_, err = file.Write(bytes.TrimSpace(value))
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func enableConntrackSettings(dir string, liberal, acct []byte) error {
	for name, old := range map[string][]byte{"nf_conntrack_tcp_be_liberal": liberal, "nf_conntrack_acct": acct} {
		if len(old) == 0 {
			continue
		}
		if err := writeExistingSysctl(filepath.Join(dir, name), []byte("1")); err != nil {
			return err
		}
	}
	return nil
}
