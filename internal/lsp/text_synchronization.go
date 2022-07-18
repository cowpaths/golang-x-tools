// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cowpaths/golang-x-tools/internal/event"
	"github.com/cowpaths/golang-x-tools/internal/jsonrpc2"
	"github.com/cowpaths/golang-x-tools/internal/lsp/protocol"
	"github.com/cowpaths/golang-x-tools/internal/lsp/source"
	"github.com/cowpaths/golang-x-tools/internal/span"
	"github.com/cowpaths/golang-x-tools/internal/xcontext"
)

// ModificationSource identifies the originating cause of a file modification.
type ModificationSource int

const (
	// FromDidOpen is a file modification caused by opening a file.
	FromDidOpen = ModificationSource(iota)

	// FromDidChange is a file modification caused by changing a file.
	FromDidChange

	// FromDidChangeWatchedFiles is a file modification caused by a change to a
	// watched file.
	FromDidChangeWatchedFiles

	// FromDidSave is a file modification caused by a file save.
	FromDidSave

	// FromDidClose is a file modification caused by closing a file.
	FromDidClose

	// FromRegenerateCgo refers to file modifications caused by regenerating
	// the cgo sources for the workspace.
	FromRegenerateCgo

	// FromInitialWorkspaceLoad refers to the loading of all packages in the
	// workspace when the view is first created.
	FromInitialWorkspaceLoad
)

func (m ModificationSource) String() string {
	switch m {
	case FromDidOpen:
		return "opened files"
	case FromDidChange:
		return "changed files"
	case FromDidChangeWatchedFiles:
		return "files changed on disk"
	case FromDidSave:
		return "saved files"
	case FromDidClose:
		return "close files"
	case FromRegenerateCgo:
		return "regenerate cgo"
	case FromInitialWorkspaceLoad:
		return "initial workspace load"
	default:
		return "unknown file modification"
	}
}

func (s *Server) didOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) error {
	uri := params.TextDocument.URI.SpanURI()
	if !uri.IsFile() {
		return nil
	}
	// There may not be any matching view in the current session. If that's
	// the case, try creating a new view based on the opened file path.
	//
	// TODO(rstambler): This seems like it would continuously add new
	// views, but it won't because ViewOf only returns an error when there
	// are no views in the session. I don't know if that logic should go
	// here, or if we can continue to rely on that implementation detail.
	if _, err := s.session.ViewOf(uri); err != nil {
		dir := filepath.Dir(uri.Filename())
		if err := s.addFolders(ctx, []protocol.WorkspaceFolder{{
			URI:  string(protocol.URIFromPath(dir)),
			Name: filepath.Base(dir),
		}}); err != nil {
			return err
		}
	}
	return s.didModifyFiles(ctx, []source.FileModification{{
		URI:        uri,
		Action:     source.Open,
		Version:    params.TextDocument.Version,
		Text:       []byte(params.TextDocument.Text),
		LanguageID: params.TextDocument.LanguageID,
	}}, FromDidOpen)
}

func (s *Server) didChange(ctx context.Context, params *protocol.DidChangeTextDocumentParams) error {
	uri := params.TextDocument.URI.SpanURI()
	if !uri.IsFile() {
		return nil
	}

	text, err := s.changedText(ctx, uri, params.ContentChanges)
	if err != nil {
		return err
	}
	c := source.FileModification{
		URI:     uri,
		Action:  source.Change,
		Version: params.TextDocument.Version,
		Text:    text,
	}
	if err := s.didModifyFiles(ctx, []source.FileModification{c}, FromDidChange); err != nil {
		return err
	}
	return s.warnAboutModifyingGeneratedFiles(ctx, uri)
}

// warnAboutModifyingGeneratedFiles shows a warning if a user tries to edit a
// generated file for the first time.
func (s *Server) warnAboutModifyingGeneratedFiles(ctx context.Context, uri span.URI) error {
	s.changedFilesMu.Lock()
	_, ok := s.changedFiles[uri]
	if !ok {
		s.changedFiles[uri] = struct{}{}
	}
	s.changedFilesMu.Unlock()

	// This file has already been edited before.
	if ok {
		return nil
	}

	// Ideally, we should be able to specify that a generated file should
	// be opened as read-only. Tell the user that they should not be
	// editing a generated file.
	view, err := s.session.ViewOf(uri)
	if err != nil {
		return err
	}
	snapshot, release := view.Snapshot(ctx)
	isGenerated := source.IsGenerated(ctx, snapshot, uri)
	release()

	if !isGenerated {
		return nil
	}
	return s.client.ShowMessage(ctx, &protocol.ShowMessageParams{
		Message: fmt.Sprintf("Do not edit this file! %s is a generated file.", uri.Filename()),
		Type:    protocol.Warning,
	})
}

func (s *Server) didChangeWatchedFiles(ctx context.Context, params *protocol.DidChangeWatchedFilesParams) error {
	var modifications []source.FileModification
	for _, change := range params.Changes {
		uri := change.URI.SpanURI()
		if !uri.IsFile() {
			continue
		}
		action := changeTypeToFileAction(change.Type)
		modifications = append(modifications, source.FileModification{
			URI:    uri,
			Action: action,
			OnDisk: true,
		})
	}
	return s.didModifyFiles(ctx, modifications, FromDidChangeWatchedFiles)
}

func (s *Server) didSave(ctx context.Context, params *protocol.DidSaveTextDocumentParams) error {
	uri := params.TextDocument.URI.SpanURI()
	if !uri.IsFile() {
		return nil
	}
	c := source.FileModification{
		URI:    uri,
		Action: source.Save,
	}
	if params.Text != nil {
		c.Text = []byte(*params.Text)
	}
	return s.didModifyFiles(ctx, []source.FileModification{c}, FromDidSave)
}

func (s *Server) didClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) error {
	uri := params.TextDocument.URI.SpanURI()
	if !uri.IsFile() {
		return nil
	}
	return s.didModifyFiles(ctx, []source.FileModification{
		{
			URI:     uri,
			Action:  source.Close,
			Version: -1,
			Text:    nil,
		},
	}, FromDidClose)
}

func (s *Server) didModifyFiles(ctx context.Context, modifications []source.FileModification, cause ModificationSource) error {
	diagnoseDone := make(chan struct{})
	if s.session.Options().VerboseWorkDoneProgress {
		work := s.progress.Start(ctx, DiagnosticWorkTitle(cause), "Calculating file diagnostics...", nil, nil)
		defer func() {
			go func() {
				<-diagnoseDone
				work.End(ctx, "Done.")
			}()
		}()
	}

	onDisk := cause == FromDidChangeWatchedFiles
	delay := s.session.Options().ExperimentalWatchedFileDelay
	s.fileChangeMu.Lock()
	defer s.fileChangeMu.Unlock()
	if !onDisk || delay == 0 {
		// No delay: process the modifications immediately.
		return s.processModifications(ctx, modifications, onDisk, diagnoseDone)
	}
	// Debounce and batch up pending modifications from watched files.
	pending := &pendingModificationSet{
		diagnoseDone: diagnoseDone,
		changes:      modifications,
	}
	// Invariant: changes appended to s.pendingOnDiskChanges are eventually
	// handled in the order they arrive. This guarantee is only partially
	// enforced here. Specifically:
	//  1. s.fileChangesMu ensures that the append below happens in the order
	//     notifications were received, so that the changes within each batch are
	//     ordered properly.
	//  2. The debounced func below holds s.fileChangesMu while processing all
	//     changes in s.pendingOnDiskChanges, ensuring that no batches are
	//     processed out of order.
	//  3. Session.ExpandModificationsToDirectories and Session.DidModifyFiles
	//     process changes in order.
	s.pendingOnDiskChanges = append(s.pendingOnDiskChanges, pending)
	ctx = xcontext.Detach(ctx)
	okc := s.watchedFileDebouncer.debounce("", 0, time.After(delay))
	go func() {
		if ok := <-okc; !ok {
			return
		}
		s.fileChangeMu.Lock()
		var allChanges []source.FileModification
		// For accurate progress notifications, we must notify all goroutines
		// waiting for the diagnose pass following a didChangeWatchedFiles
		// notification. This is necessary for regtest assertions.
		var dones []chan struct{}
		for _, pending := range s.pendingOnDiskChanges {
			allChanges = append(allChanges, pending.changes...)
			dones = append(dones, pending.diagnoseDone)
		}

		allDone := make(chan struct{})
		if err := s.processModifications(ctx, allChanges, onDisk, allDone); err != nil {
			event.Error(ctx, "processing delayed file changes", err)
		}
		s.pendingOnDiskChanges = nil
		s.fileChangeMu.Unlock()
		<-allDone
		for _, done := range dones {
			close(done)
		}
	}()
	return nil
}

// processModifications update server state to reflect file changes, and
// triggers diagnostics to run asynchronously. The diagnoseDone channel will be
// closed once diagnostics complete.
func (s *Server) processModifications(ctx context.Context, modifications []source.FileModification, onDisk bool, diagnoseDone chan struct{}) error {
	s.stateMu.Lock()
	if s.state >= serverShutDown {
		// This state check does not prevent races below, and exists only to
		// produce a better error message. The actual race to the cache should be
		// guarded by Session.viewMu.
		s.stateMu.Unlock()
		close(diagnoseDone)
		return errors.New("server is shut down")
	}
	s.stateMu.Unlock()
	// If the set of changes included directories, expand those directories
	// to their files.
	modifications = s.session.ExpandModificationsToDirectories(ctx, modifications)

	snapshots, release, err := s.session.DidModifyFiles(ctx, modifications)
	if err != nil {
		close(diagnoseDone)
		return err
	}

	go func() {
		s.diagnoseSnapshots(snapshots, onDisk)
		release()
		close(diagnoseDone)
	}()

	// After any file modifications, we need to update our watched files,
	// in case something changed. Compute the new set of directories to watch,
	// and if it differs from the current set, send updated registrations.
	return s.updateWatchedDirectories(ctx)
}

// DiagnosticWorkTitle returns the title of the diagnostic work resulting from a
// file change originating from the given cause.
func DiagnosticWorkTitle(cause ModificationSource) string {
	return fmt.Sprintf("diagnosing %v", cause)
}

func (s *Server) changedText(ctx context.Context, uri span.URI, changes []protocol.TextDocumentContentChangeEvent) ([]byte, error) {
	if len(changes) == 0 {
		return nil, fmt.Errorf("%w: no content changes provided", jsonrpc2.ErrInternal)
	}

	// Check if the client sent the full content of the file.
	// We accept a full content change even if the server expected incremental changes.
	if len(changes) == 1 && changes[0].Range == nil && changes[0].RangeLength == 0 {
		return []byte(changes[0].Text), nil
	}
	return s.applyIncrementalChanges(ctx, uri, changes)
}

func (s *Server) applyIncrementalChanges(ctx context.Context, uri span.URI, changes []protocol.TextDocumentContentChangeEvent) ([]byte, error) {
	fh, err := s.session.GetFile(ctx, uri)
	if err != nil {
		return nil, err
	}
	content, err := fh.Read()
	if err != nil {
		return nil, fmt.Errorf("%w: file not found (%v)", jsonrpc2.ErrInternal, err)
	}
	for _, change := range changes {
		// Make sure to update column mapper along with the content.
		m := protocol.NewColumnMapper(uri, content)
		if change.Range == nil {
			return nil, fmt.Errorf("%w: unexpected nil range for change", jsonrpc2.ErrInternal)
		}
		spn, err := m.RangeSpan(*change.Range)
		if err != nil {
			return nil, err
		}
		if !spn.HasOffset() {
			return nil, fmt.Errorf("%w: invalid range for content change", jsonrpc2.ErrInternal)
		}
		start, end := spn.Start().Offset(), spn.End().Offset()
		if end < start {
			return nil, fmt.Errorf("%w: invalid range for content change", jsonrpc2.ErrInternal)
		}
		var buf bytes.Buffer
		buf.Write(content[:start])
		buf.WriteString(change.Text)
		buf.Write(content[end:])
		content = buf.Bytes()
	}
	return content, nil
}

func changeTypeToFileAction(ct protocol.FileChangeType) source.FileAction {
	switch ct {
	case protocol.Changed:
		return source.Change
	case protocol.Created:
		return source.Create
	case protocol.Deleted:
		return source.Delete
	}
	return source.UnknownFileAction
}
