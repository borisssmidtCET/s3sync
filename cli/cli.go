package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/alexflint/go-arg"
	"github.com/larrabee/s3sync/storage"
	"github.com/mattn/go-isatty"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Parsed CLI args with embedded fields
type argsParsed struct {
	args
	Source             connect
	Target             connect
	S3RetryInterval    time.Duration
	FSFilePerm         os.FileMode
	FSDirPerm          os.FileMode
	RateLimitBandwidth int
	ErrorHandlingMask  storage.ErrHandlingMask
}

type connect struct {
	Type   storage.Type
	Bucket string
	Path   string
}

// Raw CLI args
type args struct {
	// Source config
	Source         string `arg:"positional"`
	SourceNoSign   bool   `arg:"--sn" help:"Don't sign request to source AWS for anonymous access"`
	SourceKey      string `arg:"--sk" help:"Source AWS key"`
	SourceSecret   string `arg:"--ss" help:"Source AWS session secret"`
	SourceToken    string `arg:"--st" help:"Source AWS token"`
	SourceRegion   string `arg:"--sr" help:"Source AWS Region"`
	SourceEndpoint string `arg:"--se" help:"Source AWS Endpoint"`
	// Target config
	Target         string `arg:"positional"`
	TargetNoSign   bool   `arg:"--tn" help:"Don't sign request to target AWS for anonymous access"`
	TargetKey      string `arg:"--tk" help:"Target AWS key"`
	TargetSecret   string `arg:"--ts" help:"Target AWS secret"`
	TargetToken    string `arg:"--tt" help:"Target AWS session token"`
	TargetRegion   string `arg:"--tr" help:"Target AWS Region"`
	TargetEndpoint string `arg:"--te" help:"Target AWS Endpoint"`
	// S3 config
	S3Retry         uint   `arg:"--s3-retry" help:"Max numbers of retries to sync file"`
	S3RetryInterval uint   `arg:"--s3-retry-sleep" help:"Sleep interval (sec) between sync retries on error"`
	S3Acl           string `arg:"--s3-acl" help:"S3 ACL for uploaded files. Possible values: private, public-read, public-read-write, aws-exec-read, authenticated-read, bucket-owner-read, bucket-owner-full-control"`
	S3StorageClass  string `arg:"--s3-storage-class" help:"S3 Storage Class for uploaded files."`
	S3KeysPerReq    int64  `arg:"--s3-keys-per-req" help:"Max numbers of keys retrieved via List request"`
	// FS config
	FSFilePerm     string `arg:"--fs-file-perm" help:"File permissions"`
	FSDirPerm      string `arg:"--fs-dir-perm" help:"Dir permissions"`
	FSDisableXattr bool   `arg:"--fs-disable-xattr" help:"Disable FS xattr for storing metadata"`
	FSAtomicWrite  bool   `arg:"--fs-atomic-write" help:"Enable FS atomic writes. New files will be written to temp file and renamed"`
	// Filters
	FilterExt         []string `arg:"--filter-ext,separate" help:"Sync only files with given extensions"`
	FilterExtNot      []string `arg:"--filter-not-ext,separate" help:"Skip files with given extensions"`
	FilterCT          []string `arg:"--filter-ct,separate" help:"Sync only files with given Content-Type"`
	FilterCTNot       []string `arg:"--filter-not-ct,separate" help:"Skip files with given Content-Type"`
	FilterMtimeAfter  int64    `arg:"--filter-after-mtime" help:"Sync only files modified after given unix timestamp"`
	FilterMtimeBefore int64    `arg:"--filter-before-mtime" help:"Sync only files modified before given unix timestamp"`
	FilterModified    bool     `arg:"--filter-modified" help:"Sync only modified files"`
	FilterExist       bool     `arg:"--filter-exist" help:"Sync only files, that exist in target storage"`
	FilterExistNot    bool     `arg:"--filter-not-exist" help:"Sync only files, that doesn't exist in target storage"`
	FilterDirs        bool     `arg:"--filter-dirs" help:"Sync only files, that ends with slash (/)"`
	FilterDirsNot     bool     `arg:"--filter-not-dirs" help:"Skip files that ends with slash (/)"`
	// Misc
	Workers           uint   `arg:"-w" help:"Workers count"`
	Debug             bool   `arg:"-d" help:"Show debug logging"`
	SyncLog           bool   `arg:"--sync-log" help:"Show sync log"`
	SyncLogFormat     string `arg:"--sync-log-format" help:"Format of sync log. Possible values: json"`
	ShowProgress      bool   `arg:"--sync-progress,-p" help:"Show sync progress"`
	OnFail            string `arg:"--on-fail,-f" help:"Action on failed. Possible values: fatal, skip, skipmissing (DEPRECATED, use --error-handling instead)"`
	ErrorHandlingMask uint8  `arg:"--error-handling" help:"Controls error handling. Sum of the values: 1 for ignoring NotFound errors, 2 for ignoring PermissionDenied errors OR 255 to ignore all errors"`
	DisableHTTP2      bool   `arg:"--disable-http2" help:"Disable HTTP2 for http client"`
	ListBuffer        uint   `arg:"--list-buffer" help:"Size of list buffer"`
	SkipSSLVerify     bool   `arg:"--skip-ssl-verify" help:"Disable SSL verification for S3"`
	// Rate Limit
	RateLimitObjPerSec uint   `arg:"--ratelimit-objects" help:"Rate limit objects per second"`
	RateLimitBandwidth string `arg:"--ratelimit-bandwidth" help:"Set bandwidth rate limit, byte/s, Allow suffixes: K, M, G"`
}

// Version return program version string on human format
func (args) Version() string {
	return fmt.Sprintf("Version: %v, commit: %v, built at: %v", version, commit, date)
}

// Description return program description string
func (args) Description() string {
	return "Really fast sync tool for S3"
}

// GetCliArgs parse cli args, set default values, check input values and return argsParsed struct
func GetCliArgs() (cli argsParsed, err error) {
	rawCli := args{}
	rawCli.Workers = 16
	rawCli.S3Retry = 0
	rawCli.S3RetryInterval = 0
	rawCli.S3Acl = ""
	rawCli.S3KeysPerReq = 1000
	rawCli.OnFail = "fatal"
	rawCli.FSDirPerm = "0755"
	rawCli.FSFilePerm = "0644"
	rawCli.ListBuffer = 1000
	rawCli.RateLimitObjPerSec = 0
	rawCli.ErrorHandlingMask = 0
	rawCli.SyncLogFormat = ""

	p := arg.MustParse(&rawCli)
	cli.args = rawCli

	cli.args.S3Acl = strings.ToLower(cli.args.S3Acl)
	switch cli.args.S3Acl {
	case "", "copy":
		break
	case "private", "public-read", "public-read-write", "aws-exec-read":
		break
	case "authenticated-read", "bucket-owner-read", "bucket-owner-full-control":
		break
	default:
		p.Fail("--acl must be one of \"copy, private, public-read, public-read-write, aws-exec-read, authenticated-read, bucket-owner-read, bucket-owner-full-control\"")
	}

	cli.ErrorHandlingMask = storage.ErrHandlingMask(cli.args.ErrorHandlingMask)
	switch cli.args.OnFail {
	case "fatal":
	case "skip":
		cli.ErrorHandlingMask = ^storage.ErrHandlingMask(0)
	case "skipmissing":
		cli.ErrorHandlingMask.Add(storage.HandleErrNotExist)
	default:
		p.Fail("--on-fail must be one of \"fatal, skip, skipmissing\"")
	}

	switch cli.args.SyncLogFormat {
	case "json", "":
	default:
		p.Fail("--sync-log-format must be one of \"json\"")
	}

	if rate, ok := parseBandwith(cli.args.RateLimitBandwidth); ok {
		cli.RateLimitBandwidth = rate
	} else {
		p.Fail("Invalid value of (--ratelimit-bandwidth) arg")
	}

	cli.S3RetryInterval = time.Duration(cli.args.S3RetryInterval) * time.Second
	if cli.Source, err = parseConn(cli.args.Source); err != nil {
		return cli, err
	}
	if cli.Target, err = parseConn(cli.args.Target); err != nil {
		return cli, err
	}
	if cli.args.ShowProgress && !isatty.IsTerminal(os.Stdout.Fd()) {
		p.Fail("Progress (--sync-progress) require tty")
	}

	if filePerm, err := strconv.ParseUint(cli.args.FSFilePerm, 8, 32); err != nil {
		p.Fail("Failed to parse arg --fs-file-perm")
	} else {
		cli.FSFilePerm = os.FileMode(filePerm)
	}

	if dirPerm, err := strconv.ParseUint(cli.args.FSDirPerm, 8, 32); err != nil {
		p.Fail("Failed to parse arg --fs-dir-perm")
	} else {
		cli.FSDirPerm = os.FileMode(dirPerm)
	}

	if cli.DisableHTTP2 {
		_ = os.Setenv("GODEBUG", os.Getenv("GODEBUG")+"http2client=0")
	}

	if cli.FilterModified && cli.FSDisableXattr {
		p.Fail("Filter modified files (--filter-modified) required xattr")
	}

	return
}

func parseConn(cStr string) (conn connect, err error) {
	u, err := url.Parse(cStr)
	if err != nil {
		return conn, err
	}

	switch u.Scheme {
	case "s3":
		conn.Type = storage.TypeS3
		conn.Bucket = u.Host
		conn.Path = strings.TrimPrefix(u.Path, "/")
	case "s3s":
		conn.Type = storage.TypeS3Stream
		conn.Bucket = u.Host
		conn.Path = strings.TrimPrefix(u.Path, "/")
	case "fs":
		conn.Type = storage.TypeFS
		conn.Path = strings.TrimPrefix(cStr, "fs://")
	default:
		conn.Type = storage.TypeFS
		conn.Path = cStr
	}
	return
}

func parseBandwith(s string) (int, bool) {
	if s == "" {
		return 0, true
	}
	s = strings.TrimSpace(s)
	digits := ""
	multiplier := 1

	for _, r := range s {
		if unicode.IsDigit(r) {
			digits += string(r)
			continue
		}
		if unicode.IsSpace(r) {
			continue
		}
		switch r {
		case 'k', 'K':
			multiplier = 1024
		case 'm', 'M':
			multiplier = 1024 * 1024
		case 'g', 'G':
			multiplier = 1024 * 1024 * 1024
		default:
			return 0, false
		}
	}
	rate, err := strconv.Atoi(digits)
	if err != nil {
		return 0, false
	}

	return rate * multiplier, true
}
