// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"golang.org/x/crypto/ssh/terminal"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

type arrayFlags []string

func (i *arrayFlags) String() string {
	return fmt.Sprintf("%v", *i)
}

func (i *arrayFlags) Set(value string) error {
	*i = strings.Split(value, ",")
	return nil
}

var command = flag.String("c", "", "command to be executed")

var concArgs arrayFlags

func main() {
	flag.Var(&concArgs, "a", "files to be executed concurrently (separated by comma)")
	flag.Parse()
	err := runAll()
	if e, ok := interp.IsExitStatus(err); ok {
		os.Exit(int(e))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runAll() error {
	seqRunner, err := interp.New(interp.StdIO(os.Stdin, os.Stdout, os.Stderr))
	if err != nil {
		return err
	}

	if *command != "" {
		return run(seqRunner, strings.NewReader(*command), "")
	}
	if flag.NArg() == 0 && flag.NFlag() == 0 {
		if terminal.IsTerminal(int(os.Stdin.Fd())) {
			return runInteractive(seqRunner, os.Stdin, os.Stdout, os.Stderr)
		}
		return run(seqRunner, os.Stdin, "")
	}

	for _, path := range flag.Args() {
		if err := runPath(seqRunner, path); err != nil {
			return err
		}
	}

	rm := interp.NewRunnersManager()
	for i := 0; i < runtime.NumCPU(); i++ {
		r, err := interp.New(interp.StdIO(os.Stdin, os.Stdout, os.Stderr), interp.Manager(rm))
		if err != nil {
			return err
		}
		rm.AddRunner(r)
	}

	if err := runPaths(rm, concArgs); err != nil {
		return err
	}

	return nil
}

func run(r *interp.Runner, reader io.Reader, name string) error {
	prog, err := syntax.NewParser().Parse(reader, name)
	if err != nil {
		return err
	}
	r.Reset()
	ctx := context.Background()
	return r.Run(ctx, prog)
}

type readerWithName struct {
	io.Reader
	name string
}

func runRmAll(rm *interp.RunnersManager, readers ...readerWithName) error {
	nodes := make([]syntax.Node, len(readers))

	for i, rWithName := range readers {
		var err error

		nodes[i], err = syntax.NewParser().Parse(rWithName.Reader, rWithName.name)
		if err != nil {
			return err
		}
	}

	ctx := context.Background()
	return rm.RunAll(ctx, nodes...)
}

func runPath(r *interp.Runner, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return run(r, f, path)
}

func runPaths(rm *interp.RunnersManager, paths []string) error {
	readers := make([]readerWithName, len(paths))
	for i, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()

		readers[i] = readerWithName{f, p}
	}

	return runRmAll(rm, readers...)
}

func runInteractive(r *interp.Runner, stdin io.Reader, stdout, stderr io.Writer) error {
	parser := syntax.NewParser()
	fmt.Fprintf(stdout, "$ ")
	var runErr error
	fn := func(stmts []*syntax.Stmt) bool {
		if parser.Incomplete() {
			fmt.Fprintf(stdout, "> ")
			return true
		}
		ctx := context.Background()
		for _, stmt := range stmts {
			runErr = r.Run(ctx, stmt)
			if r.Exited() {
				return false
			}
		}
		fmt.Fprintf(stdout, "$ ")
		return true
	}
	if err := parser.Interactive(stdin, fn); err != nil {
		return err
	}
	return runErr
}
