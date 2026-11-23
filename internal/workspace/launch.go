package workspace

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RecordLaunchInvite appends a confirmed owner e-mail to the central
// launch-invitation list (<dataDir>/launch_invites.tsv), once per
// address. Workspaces are reclaimed when idle, so the list lives outside
// them. A no-op in local mode, where the owner row in the database is
// the record.
func (m *Manager) RecordLaunchInvite(email string) error {
	if m.local != nil || m.dir == "" {
		return nil
	}
	email = strings.ToLower(strings.TrimSpace(email))
	path := filepath.Join(filepath.Dir(m.dir), "launch_invites.tsv")
	m.inviteMu.Lock()
	defer m.inviteMu.Unlock()
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if strings.SplitN(sc.Text(), "\t", 2)[0] == email {
				f.Close()
				return nil
			}
		}
		f.Close()
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(email + "\t" + time.Now().UTC().Format(time.RFC3339) + "\n")
	return err
}
