package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/releasegate"
)

const maxRecordBytes = 4 << 20

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) != 2 || (args[0] != "candidate" && args[0] != "final") {
		return errors.New("usage: releasegate candidate|final RECORD.json")
	}
	record, err := readRecord(args[1])
	if err != nil {
		return err
	}
	if args[0] == "candidate" {
		err = releasegate.ValidateCandidate(record)
	} else {
		err = releasegate.ValidateFinal(record)
	}
	if err != nil {
		return fmt.Errorf("validate %s release gates: %w", args[0], err)
	}
	_, err = fmt.Fprintf(stdout, "%s %s\n", args[0], record.Candidate.Commit)
	return err
}

func readRecord(path string) (releasegate.Record, error) {
	info, err := os.Lstat(path) //nolint:gosec // path is the operator's explicit release evidence input.
	if err != nil {
		return releasegate.Record{}, fmt.Errorf("read release gate record: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxRecordBytes {
		return releasegate.Record{}, errors.New("read release gate record: input must be a bounded non-symlink regular file")
	}
	file, err := os.Open(path) //nolint:gosec // Lstat above confines the explicit bounded non-symlink file shape.
	if err != nil {
		return releasegate.Record{}, fmt.Errorf("read release gate record: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || openedInfo.Size() != info.Size() || !os.SameFile(info, openedInfo) {
		return releasegate.Record{}, errors.New("read release gate record: input identity changed while opening")
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxRecordBytes+1))
	decoder.DisallowUnknownFields()
	var record releasegate.Record
	if err := decoder.Decode(&record); err != nil {
		return releasegate.Record{}, fmt.Errorf("read release gate record: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return releasegate.Record{}, errors.New("read release gate record: trailing JSON is forbidden")
	}
	return record, nil
}
