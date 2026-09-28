package tui

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/hoveychen/netferry/relay/internal/profile"
	"github.com/hoveychen/netferry/relay/internal/sshconfig"
	"github.com/hoveychen/netferry/relay/internal/store"
)

// defaultLatencyBuffer is newProfile()'s latencyBufferSize.
const defaultLatencyBuffer uint32 = 2097152

func boolPtr(b bool) *bool { return &b }

// NewProfile mirrors profileStore.ts newProfile() + buildBlankProfile (the
// identity file is prefilled from `Host *` in ~/.ssh/config).
func NewProfile(home string) profile.Profile {
	lb := defaultLatencyBuffer
	p := profile.Profile{
		ID:                store.NewID(),
		Name:              "New Profile",
		Subnets:           []string{"0.0.0.0/0"},
		Dns:               profile.DnsAll,
		ExcludeSubnets:    []string{},
		Method:            "auto",
		BlockUDP:          boolPtr(true),
		AutoExcludeLAN:    boolPtr(true),
		PoolSize:          4,
		LatencyBufferSize: &lb,
	}
	if home != "" {
		p.IdentityFile = sshconfig.DefaultIdentityFile(home)
	}
	return p
}

// ProfileFromSSH mirrors SshConfigImporter.handleImport: a new profile named
// after the Host alias, with remote/identity/jump hosts resolved. Unlike a
// .nfprofile import it is not marked imported (the fields stay editable).
func ProfileFromSSH(e sshconfig.HostEntry, all []sshconfig.HostEntry) profile.Profile {
	p := NewProfile("")
	p.IdentityFile = ""
	sshconfig.ApplyToProfile(&p, e, all)
	p.Imported = false
	return p
}

// AddProfile saves a new profile and attaches it to the active group
// (App.tsx attachNewProfilesToActiveGroup).
func (d *Data) AddProfile(p profile.Profile) error {
	if _, err := store.UpsertProfile(p); err != nil {
		return err
	}
	g, err := d.freshActiveGroup()
	if err != nil || g == nil {
		return err
	}
	g.ChildrenIDs = append(g.ChildrenIDs, p.ID)
	return store.SaveGroup(g)
}

// freshActiveGroup re-reads the active group from disk, so read-modify-write
// edits never overwrite a change made since d was loaded (the desktop may be
// editing the same store).
func (d *Data) freshActiveGroup() (*store.Group, error) {
	if d.Settings.ActiveGroupID == "" {
		return nil, nil
	}
	return store.LoadGroup(d.Settings.ActiveGroupID)
}

// UpdateProfile saves an existing profile in place.
func (d *Data) UpdateProfile(p profile.Profile) error {
	_, err := store.UpsertProfile(p)
	return err
}

// DeleteProfile removes the profile. Like the desktop it is not removed from
// groups (joins skip unknown ids).
func (d *Data) DeleteProfile(id string) error {
	_, err := store.RemoveProfile(id)
	return err
}

// RemoveFromActiveGroup drops id from the active group's children.
func (d *Data) RemoveFromActiveGroup(id string) error {
	g, err := d.freshActiveGroup()
	if err != nil || g == nil {
		return err
	}
	kept := []string{}
	for _, c := range g.ChildrenIDs {
		if c != id {
			kept = append(kept, c)
		}
	}
	if len(kept) == len(g.ChildrenIDs) {
		return nil
	}
	g.ChildrenIDs = kept
	return store.SaveGroup(g)
}

// SelectGroup makes id the active group.
func (d *Data) SelectGroup(id string) error {
	s := d.Settings
	s.ActiveGroupID = id
	return store.SaveSettings(s)
}

// CreateGroup saves an empty "New Group" (groupStore.ts newGroup) and
// activates it.
func (d *Data) CreateGroup() (string, error) {
	g := &store.Group{ID: store.NewID(), Name: "New Group", ChildrenIDs: []string{},
		Rules: map[string]store.RouteMode{}, Priorities: map[string]int{}}
	if err := store.SaveGroup(g); err != nil {
		return "", err
	}
	return g.ID, d.SelectGroup(g.ID)
}

// RenameActiveGroup renames the active group.
func (d *Data) RenameActiveGroup(name string) error {
	g, err := d.freshActiveGroup()
	if err != nil || g == nil {
		return err
	}
	g.Name = name
	return store.SaveGroup(g)
}

// DeleteActiveGroup deletes every child profile, then the group, then
// activates the first surviving group (handleDeleteActiveGroup).
func (d *Data) DeleteActiveGroup() error {
	g, err := d.freshActiveGroup()
	if err != nil || g == nil {
		return err
	}
	var firstErr error
	for _, id := range g.ChildrenIDs {
		if err := d.DeleteProfile(id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := store.DeleteGroup(g.ID); err != nil {
		return err
	}
	next := ""
	for _, o := range d.Groups {
		if o.ID != g.ID {
			next = o.ID
			break
		}
	}
	if err := d.SelectGroup(next); err != nil {
		return err
	}
	return firstErr
}

// ── export / import ─────────────────────────────────────────────────────────

// CanShare mirrors the desktop's gate for the export actions: every identity
// slot (main + each jump host) has a key or a file.
func CanShare(p profile.Profile) bool {
	has := func(key, file string) bool { return strings.TrimSpace(key) != "" || strings.TrimSpace(file) != "" }
	if !has(p.IdentityKey, p.IdentityFile) {
		return false
	}
	for _, j := range p.JumpHosts {
		if !has(j.IdentityKey, j.IdentityFile) {
			return false
		}
	}
	return true
}

// inlineIdentities mirrors commands.rs inline_identities_for_export: file
// identities are read into identityKey and the path cleared, so the export
// is self-contained.
func inlineIdentities(p *profile.Profile, home string) error {
	needs := func(key, file string) bool { return strings.TrimSpace(key) == "" && strings.TrimSpace(file) != "" }
	if needs(p.IdentityKey, p.IdentityFile) {
		path := sshconfig.ExpandTilde(strings.TrimSpace(p.IdentityFile), home)
		pem, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read identity file %s: %w", path, err)
		}
		p.IdentityKey, p.IdentityFile = string(pem), ""
	}
	jumps := append([]profile.JumpHost(nil), p.JumpHosts...)
	for i := range jumps {
		if needs(jumps[i].IdentityKey, jumps[i].IdentityFile) {
			path := sshconfig.ExpandTilde(strings.TrimSpace(jumps[i].IdentityFile), home)
			pem, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("failed to read jump host %d identity file %s: %w", i+1, path, err)
			}
			jumps[i].IdentityKey, jumps[i].IdentityFile = string(pem), ""
		}
	}
	p.JumpHosts = jumps
	return nil
}

// ExportProfile returns the encrypted .nfprofile payload for p.
func ExportProfile(p profile.Profile, home string) (string, error) {
	if err := inlineIdentities(&p, home); err != nil {
		return "", err
	}
	raw, err := json.Marshal(store.NormalizeProfile(p))
	if err != nil {
		return "", err
	}
	return profile.Encrypt(raw)
}

// DecodeProfile decrypts an .nfprofile payload into a new, imported profile
// (fresh id, imported=true — import_profile in commands.rs).
func DecodeProfile(data string) (profile.Profile, error) {
	plain, err := profile.Decrypt(strings.TrimSpace(data))
	if err != nil {
		return profile.Profile{}, err
	}
	var p profile.Profile
	if err := json.Unmarshal(plain, &p); err != nil {
		return profile.Profile{}, fmt.Errorf("invalid profile data: %w", err)
	}
	p.ID = store.NewID()
	p.Imported = true
	return p, nil
}

// qrChunkSize is QrCodeExportDialog's CHUNK_SIZE.
const qrChunkSize = 1000

// QRChunks splits an export payload into "NF:i/N:slice" QR payloads.
func QRChunks(data string) []string {
	total := (len(data) + qrChunkSize - 1) / qrChunkSize
	out := make([]string, 0, total)
	for i := 0; i < total; i++ {
		end := (i + 1) * qrChunkSize
		if end > len(data) {
			end = len(data)
		}
		out = append(out, fmt.Sprintf("NF:%d/%d:%s", i+1, total, data[i*qrChunkSize:end]))
	}
	return out
}

// osc52 is the terminal escape that sets the system clipboard (supported by
// most modern terminals, also over SSH).
func osc52(s string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(s)) + "\a"
}
