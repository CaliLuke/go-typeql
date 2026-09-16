package gotype

import (
	"strconv"
	"strings"
)

type projectionKey struct {
	shape   string
	varName string
}

type projectionEntry struct {
	signature string
	additions string
	fetch     string
}

// Projection entries belong to each ModelInfo, not the global registry. A
// replacement registration gets a new cache, and ClearRegistry drops registry
// ownership. Signatures detect sequential edits to exposed Fields/Roles and
// changes to role-player metadata across registry replacement.
func cachedProjection(info *ModelInfo, shape, varName, signature string, build func() (string, string, error)) (string, string, error) {
	key := projectionKey{shape: shape, varName: varName}
	info.projectionMu.Lock()
	entry, found := info.projections[key]
	info.projectionMu.Unlock()
	if found && entry.signature == signature {
		return entry.additions, entry.fetch, nil
	}
	additions, fetch, err := build()
	if err != nil {
		return "", "", err
	}
	info.projectionMu.Lock()
	if info.projections == nil {
		info.projections = make(map[projectionKey]projectionEntry)
	}
	info.projections[key] = projectionEntry{signature: signature, additions: additions, fetch: fetch}
	info.projectionMu.Unlock()
	return additions, fetch, nil
}

func projectionSignature(info *ModelInfo, players []*ModelInfo) string {
	var b strings.Builder
	appendFieldSignature(&b, info.Fields)
	if players != nil {
		for i, role := range info.Roles {
			appendSignaturePart(&b, role.RoleName)
			appendSignaturePart(&b, role.PlayerTypeName)
			if player := players[i]; player != nil {
				b.WriteByte('1')
				appendFieldSignature(&b, player.Fields)
			} else {
				b.WriteByte('0')
			}
		}
	}
	return b.String()
}

func projectionRolePlayers(info *ModelInfo) []*ModelInfo {
	players := make([]*ModelInfo, len(info.Roles))
	for i, role := range info.Roles {
		players[i], _ = Lookup(role.PlayerTypeName)
	}
	return players
}

func polymorphicProjectionSignature(info *ModelInfo, subtypes []*ModelInfo) string {
	var b strings.Builder
	appendFieldSignature(&b, info.Fields)
	b.WriteString(strconv.Itoa(len(subtypes)))
	b.WriteByte(':')
	for _, sub := range subtypes {
		appendSignaturePart(&b, sub.TypeName)
		appendFieldSignature(&b, sub.Fields)
	}
	return b.String()
}

func appendFieldSignature(b *strings.Builder, fields []FieldInfo) {
	b.WriteString(strconv.Itoa(len(fields)))
	b.WriteByte(':')
	for _, fi := range fields {
		appendSignaturePart(b, fi.Tag.Name)
		if fi.IsSlice {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
	}
}

func appendSignaturePart(b *strings.Builder, value string) {
	b.WriteString(strconv.Itoa(len(value)))
	b.WriteByte(':')
	b.WriteString(value)
}
