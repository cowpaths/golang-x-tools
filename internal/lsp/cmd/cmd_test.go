// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd_test

import (
	"os"
	"testing"

	"github.com/cowpaths/golang-x-tools/internal/lsp/bug"
	cmdtest "github.com/cowpaths/golang-x-tools/internal/lsp/cmd/test"
	"github.com/cowpaths/golang-x-tools/internal/lsp/tests"
	"github.com/cowpaths/golang-x-tools/internal/testenv"
)

func TestMain(m *testing.M) {
	bug.PanicOnBugs = true
	testenv.ExitIfSmallMachine()
	os.Exit(m.Run())
}

func TestCommandLine(t *testing.T) {
	cmdtest.TestCommandLine(t, "../testdata", tests.DefaultOptions)
}
