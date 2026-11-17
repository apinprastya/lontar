package main

import (
	"path/filepath"
	"strings"

	"lontar/internal/services"
)

// EventFilesDropped is sent to the frontend with the paths of the .sql
// files dropped onto the workspace; each opens in a query tab.
const EventFilesDropped = "files:dropped"

// droppedSQLFiles returns the .sql files among the dropped paths (other
// files are ignored).
func droppedSQLFiles(paths []string) []string {
	var out []string
	for _, p := range paths {
		if strings.EqualFold(filepath.Ext(p), ".sql") {
			out = append(out, p)
		}
	}
	return out
}

// emitDroppedSQLFiles tells the frontend about dropped .sql files (nothing
// when there are none).
func emitDroppedSQLFiles(em services.Emitter, paths []string) {
	if files := droppedSQLFiles(paths); len(files) > 0 {
		em.Emit(EventFilesDropped, files)
	}
}
