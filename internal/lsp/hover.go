// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsp

import (
	"context"

	"github.com/cowpaths/golang-x-tools/internal/lsp/mod"
	"github.com/cowpaths/golang-x-tools/internal/lsp/protocol"
	"github.com/cowpaths/golang-x-tools/internal/lsp/source"
	"github.com/cowpaths/golang-x-tools/internal/lsp/template"
	"github.com/cowpaths/golang-x-tools/internal/lsp/work"
)

func (s *Server) hover(ctx context.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	snapshot, fh, ok, release, err := s.beginFileRequest(ctx, params.TextDocument.URI, source.UnknownKind)
	defer release()
	if !ok {
		return nil, err
	}
	switch snapshot.View().FileKind(fh) {
	case source.Mod:
		return mod.Hover(ctx, snapshot, fh, params.Position)
	case source.Go:
		return source.Hover(ctx, snapshot, fh, params.Position)
	case source.Tmpl:
		return template.Hover(ctx, snapshot, fh, params.Position)
	case source.Work:
		return work.Hover(ctx, snapshot, fh, params.Position)
	}
	return nil, nil
}
