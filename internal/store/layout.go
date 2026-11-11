package store

import (
	"io/fs"
	"path"
	"strings"
)

// The vault's layout: a secret lives at <group>/<slug>.age, where <group> is
// the Group's folder. SecretID is the one rule for which file a secret lives
// in, and groupFolders the one rule for which of a vault's files are secret
// files; Contents, CopyTo, Exists and migration all use it, so they agree on
// what belongs to the vault.

// SecretID identifies the file a Secret is stored in: its Group's folder and
// the file name its name gives. Secrets with equal SecretIDs share one file,
// so saving one replaces the other: names that differ only in case or
// punctuation ("GitHub Key", "github-key") have the same SecretID, as do
// differently spelled labels of one group ("Work", "work"). SecretIDs are
// comparable with == and can be map keys.
type SecretID struct {
	group GroupID
	slug  string
}

// ID returns the SecretID of the file the secret is stored in, given its
// Name and Group.
func (s *Secret) ID() SecretID {
	return secretID(s.Name, s.Group)
}

// Group returns the ID of the group folder the secret's file is in.
func (id SecretID) Group() GroupID {
	return id.group
}

func secretID(name, group string) SecretID {
	return SecretID{group: GroupIDOf(group), slug: Slugify(name)}
}

// file returns the secret's vault-relative path, <group>/<slug>.age. Slugs
// only hold [a-z0-9-], so the path cannot leave the vault.
func (id SecretID) file() string {
	return path.Join(id.group.folder, id.slug+".age")
}

// GroupID identifies a Group's folder. A group's label is kept as typed, so
// labels that differ only in case or punctuation ("Personal Finance",
// "personal-finance") name one group, stored in one folder, and have equal
// GroupIDs. A label with no ASCII letter or digit names DefaultGroup.
// GroupIDs are comparable with == and can be map keys.
type GroupID struct {
	folder string
}

// GroupIDOf returns the ID of the folder the group labelled label is
// stored in.
func GroupIDOf(label string) GroupID {
	return GroupID{folder: NormalizeGroup(label)}
}

// Contains reports whether query, spelled as a group's folder is (so
// "personal finance" and "Personal-Finance" alike), is part of this group's
// folder name. A query with no ASCII letter or digit matches nothing.
func (g GroupID) Contains(query string) bool {
	q := Slugify(query)
	return q != "" && strings.Contains(g.folder, q)
}

// groupFolder is a folder that holds a Group's secrets, as groupFolders
// found it.
type groupFolder struct {
	// name is the folder's name, relative to the vault.
	name string
	// files are the vault-relative paths of its secret files, in
	// directory order.
	files []string
	// err says why the folder couldn't be listed; files is then empty.
	err error
}

// groupFolders lists the vault's group folders and the secret files in
// each, in directory order. A group folder is a directory at the top of the
// vault whose name does not start with a dot (isGroupDir), unless it holds
// another vault (hasVaultMarker): a vault kept inside this one, such as a
// backup copy, is not one of its groups, so none of its files are counted,
// read, copied or migrated. A secret file is a regular .age file in a group
// folder. An error listing the vault itself is returned as is, so callers
// can tell a missing vault (fs.ErrNotExist) from a failure; an error listing
// one folder is kept in that folder's err, and the caller decides whether it
// fails the whole walk.
//
// Nested vaults without vlt.json (format 1, or a copy that lost it) can't be
// told from a group by layout alone, because a secret named "vlt" is stored
// in its group as vlt.age; isSecretEnvelope keeps migration from taking
// their files for secrets, and Contents reports them as unreadable.
func groupFolders(fsys vaultFS) ([]groupFolder, error) {
	entries, err := fsys.ReadDir(".")
	if err != nil {
		return nil, err //nolint:wrapcheck // callers wrap, and check for fs.ErrNotExist
	}
	var groups []groupFolder
	for _, e := range entries {
		if !isGroupDir(e) {
			continue
		}
		g := groupFolder{name: e.Name()}
		files, err := fsys.ReadDir(g.name)
		if err != nil {
			g.err = err
			groups = append(groups, g)
			continue
		}
		if hasVaultMarker(files) {
			continue
		}
		for _, f := range files {
			if !f.IsDir() && path.Ext(f.Name()) == ".age" {
				g.files = append(g.files, path.Join(g.name, f.Name()))
			}
		}
		groups = append(groups, g)
	}
	return groups, nil
}

// secretFiles returns the vault-relative path of every secret file (see
// groupFolders), failing if any group folder can't be listed.
func secretFiles(fsys vaultFS) ([]string, error) {
	groups, err := groupFolders(fsys)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, g := range groups {
		if g.err != nil {
			return nil, g.err
		}
		paths = append(paths, g.files...)
	}
	return paths, nil
}

// isGroupDir reports whether a vault directory entry can be a group: a
// directory whose name does not start with a dot. Group directories are
// slugs ([a-z0-9-]), so dot-folders are never groups; vlt's own temp folder
// on a server (.vlt-tmp) and folders such as .git are skipped.
func isGroupDir(e fs.DirEntry) bool {
	return e.IsDir() && !strings.HasPrefix(e.Name(), ".")
}

// hasVaultMarker reports whether a directory with these entries holds a
// format marker or a migration in progress. Neither name can be a secret's
// file, so such a directory is a vault, never a group.
func hasVaultMarker(entries []fs.DirEntry) bool {
	for _, e := range entries {
		if e.Name() == formatFile || e.Name() == migratingFile {
			return true
		}
	}
	return false
}
