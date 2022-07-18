// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unmarshal_test

import (
	"testing"

	"github.com/cowpaths/golang-x-tools/go/analysis/analysistest"
	"github.com/cowpaths/golang-x-tools/go/analysis/passes/unmarshal"
	"github.com/cowpaths/golang-x-tools/internal/typeparams"
)

func Test(t *testing.T) {
	testdata := analysistest.TestData()
	tests := []string{"a"}
	if typeparams.Enabled {
		tests = append(tests, "typeparams")
	}
	analysistest.Run(t, testdata, unmarshal.Analyzer, tests...)
}
