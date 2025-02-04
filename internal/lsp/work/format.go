// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"context"

	"github.com/cowpaths/golang-x-tools/internal/event"
	"github.com/cowpaths/golang-x-tools/internal/lsp/protocol"
	"github.com/cowpaths/golang-x-tools/internal/lsp/source"
	"golang.org/x/mod/modfile"
)

func Format(ctx context.Context, snapshot source.Snapshot, fh source.FileHandle) ([]protocol.TextEdit, error) {
	ctx, done := event.Start(ctx, "work.Format")
	defer done()

	pw, err := snapshot.ParseWork(ctx, fh)
	if err != nil {
		return nil, err
	}
	formatted := modfile.Format(pw.File.Syntax)
	// Calculate the edits to be made due to the change.
	diff, err := snapshot.View().Options().ComputeEdits(fh.URI(), string(pw.Mapper.Content), string(formatted))
	if err != nil {
		return nil, err
	}
	return source.ToProtocolEdits(pw.Mapper, diff)
}
