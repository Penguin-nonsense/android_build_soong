// Copyright 2018 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cc

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"

	"android/soong/android"
)

// This singleton generates a compile_commands.json file. It does so for each
// blueprint Android.bp resulting in a cc.Module when either make, mm, mma, mmm
// or mmma is called. It will only create a single compile_commands.json file
// at out/development/ide/compdb/compile_commands.json. It will also symlink it
// to ${SOONG_LINK_COMPDB_TO} if set. In general this should be created by running
// make SOONG_GEN_COMPDB=1 nothing to get all targets.

func init() {
	android.RegisterSingletonType("compdb_generator", compDBGeneratorSingleton)
}

func compDBGeneratorSingleton() android.Singleton {
	return &compdbGeneratorSingleton{}
}

type compdbGeneratorSingleton struct{}

const (
	compdbFilename                = "compile_commands.json"
	compdbOutputProjectsDirectory = "out/development/ide/compdb"

	// Environment variables used to modify behavior of this singleton.
	envVariableGenerateCompdb          = "SOONG_GEN_COMPDB"
	envVariableGenerateCompdbDebugInfo = "SOONG_GEN_COMPDB_DEBUG"
	envVariableCompdbLink              = "SOONG_LINK_COMPDB_TO"
)

// A compdb entry. The compile_commands.json file is a list of these.
type compDbEntry struct {
	Directory string   `json:"directory"`
	Arguments []string `json:"arguments"`
	File      string   `json:"file"`
	Output    string   `json:"output,omitempty"`
}

func (c *compdbGeneratorSingleton) GenerateBuildActions(ctx android.SingletonContext) {
	if !ctx.Config().IsEnvTrue(envVariableGenerateCompdb) {
		return
	}

	// Instruct the generator to indent the json file for easier debugging.
	outputCompdbDebugInfo := ctx.Config().IsEnvTrue(envVariableGenerateCompdbDebugInfo)

	// We only want one entry per file. We don't care what module/isa it's from
	m := make(map[string]compDbEntry)
	ctx.VisitAllModules(func(module android.Module) {
		if ccModule, ok := module.(*Module); ok {
			if compiledModule, ok := ccModule.compiler.(CompiledInterface); ok {
				generateCompdbProject(compiledModule, ctx, ccModule, m)
			}
		}
	})

	// Create the output file.
	dir := filepath.Join(getCompdbAndroidSrcRootDirectory(ctx), compdbOutputProjectsDirectory)
	os.MkdirAll(dir, 0777)
	compDBFile := filepath.Join(dir, compdbFilename)
	f, err := os.Create(compdbFilename)
	if err != nil {
		log.Fatalf("Could not create file %s: %s", filepath.Join(dir, compdbFilename), err)
	}
	defer f.Close()

	v := make([]compDbEntry, 0, len(m))

	for _, value := range m {
		v = append(v, value)
	}
	var dat []byte
	if outputCompdbDebugInfo {
		dat, err = json.MarshalIndent(v, "", " ")
	} else {
		dat, err = json.Marshal(v)
	}
	if err != nil {
		log.Fatalf("Failed to marshal: %s", err)
	}
	f.Write(dat)

	finalLinkPath := filepath.Join(ctx.Config().Getenv(envVariableCompdbLink), compdbFilename)
	if finalLinkPath != "" {
		os.Remove(finalLinkPath)
		if err := os.Symlink(compDBFile, finalLinkPath); err != nil {
			log.Fatalf("Unable to symlink %s to %s: %s", compDBFile, finalLinkPath, err)
		}
	}
}

func expandAllVars(ctx android.SingletonContext, args []string) []string {
	var out []string
	for _, arg := range args {
		if arg != "" {
			if val, err := evalAndSplitVariable(ctx, arg); err == nil {
				out = append(out, val...)
			} else {
				out = append(out, arg)
			}
		}
	}
	return out
}

func getArguments(src android.Path, ctx android.SingletonContext, ccModule *Module) []string {
	var args []string
	isCpp := false
	isAsm := false
	// TODO It would be better to ask soong for the types here.
	switch src.Ext() {
	case ".S", ".s", ".asm":
		isAsm = true
		isCpp = false
	case ".c":
		isAsm = false
		isCpp = false
	case ".cpp", ".cc", ".mm":
		isAsm = false
		isCpp = true
	default:
		log.Print("Unknown file extension " + src.Ext() + " on file " + src.String())
		isAsm = true
		isCpp = false
	}
	// The executable for the compilation doesn't matter but we need something there.
	args = append(args, "/bin/false")
	args = append(args, expandAllVars(ctx, ccModule.flags.GlobalFlags)...)
	args = append(args, expandAllVars(ctx, ccModule.flags.CFlags)...)
	if isCpp {
		args = append(args, expandAllVars(ctx, ccModule.flags.CppFlags)...)
	} else if !isAsm {
		args = append(args, expandAllVars(ctx, ccModule.flags.ConlyFlags)...)
	}
	args = append(args, expandAllVars(ctx, ccModule.flags.SystemIncludeFlags)...)
	args = append(args, src.String())
	return args
}

func generateCompdbProject(compiledModule CompiledInterface, ctx android.SingletonContext, ccModule *Module, builds map[string]compDbEntry) {
	srcs := compiledModule.Srcs()
	if len(srcs) == 0 {
		return
	}

	rootDir := getCompdbAndroidSrcRootDirectory(ctx)
	for _, src := range srcs {
		if _, ok := builds[src.String()]; !ok {
			builds[src.String()] = compDbEntry{
				Directory: rootDir,
				Arguments: getArguments(src, ctx, ccModule),
				File:      src.String(),
			}
		}
	}
}

func evalAndSplitVariable(ctx android.SingletonContext, str string) ([]string, error) {
	evaluated, err := ctx.Eval(pctx, str)
	if err == nil {
		return strings.Fields(evaluated), nil
	}
	return []string{""}, err
}

func getCompdbAndroidSrcRootDirectory(ctx android.SingletonContext) string {
	srcPath, _ := filepath.Abs(android.PathForSource(ctx).String())
	return srcPath
}
