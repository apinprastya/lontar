package ddl

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"lontar/internal/driver"
)

// Fingerprint identifies the structure of a table: its name, comment,
// columns, indexes, foreign keys, checks and triggers (not row estimates or
// size). Nil and empty slices hash the same, so a detail that went through
// the frontend and back has the fingerprint it had.
func Fingerprint(d *driver.TableDetail) string {
	if d == nil {
		return ""
	}
	type col struct {
		Name, Type, Default, Identity, Generated, GenExpr, OnUpdate, Collation, Comment string
		Nullable, PK, Auto                                                              bool
		PKPos                                                                           int
		Enum                                                                            []string
	}
	type idx struct {
		Name, Method, Predicate string
		Columns                 []string
		Unique, Primary         bool
	}
	type fk struct {
		Name, RefSchema, RefTable, OnUpdate, OnDelete string
		Columns, RefColumns                           []string
	}
	str := func(p *string) string {
		if p == nil {
			return "\x00nil"
		}
		return *p
	}
	nonNil := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	v := struct {
		Schema, Name, Comment string
		Columns               []col
		Indexes               []idx
		FKs                   []fk
		Checks                [][2]string
		Triggers              [][3]string
	}{Schema: d.Schema, Name: d.Name, Comment: d.Comment, Columns: []col{}, Indexes: []idx{}, FKs: []fk{}, Checks: [][2]string{}, Triggers: [][3]string{}}
	for _, c := range d.Columns {
		v.Columns = append(v.Columns, col{c.Name, c.DataType, str(c.Default), c.Identity, c.Generated, str(c.GenerationExpr), c.OnUpdate,
			c.Collation, c.Comment, c.Nullable, c.IsPrimaryKey, c.AutoIncrement, c.PKPosition, nonNil(c.EnumValues)})
	}
	for _, i := range d.Indexes {
		v.Indexes = append(v.Indexes, idx{i.Name, i.Method, i.Predicate, nonNil(i.Columns), i.Unique, i.Primary})
	}
	for _, f := range d.ForeignKeys {
		v.FKs = append(v.FKs, fk{f.Name, f.RefSchema, f.RefTable, f.OnUpdate, f.OnDelete, nonNil(f.Columns), nonNil(f.RefColumns)})
	}
	for _, c := range d.Checks {
		v.Checks = append(v.Checks, [2]string{c.Name, c.Expression})
	}
	for _, t := range d.Triggers {
		v.Triggers = append(v.Triggers, [3]string{t.Name, t.Timing, t.Event})
	}
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
