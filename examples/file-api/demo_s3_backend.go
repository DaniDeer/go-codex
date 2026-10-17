package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http/httptest"

	adapters3 "github.com/DaniDeer/go-codex/adapters/s3"
	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/examples/file-api/resources"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
)

const demoS3Bucket = "file-api-example-bucket"

// demoS3Backend proves Phase 1's core promise concretely: the EXACT
// SAME [resources.Notes] declaration used throughout every other demo
// in this example works UNCHANGED against a completely different
// backend — [adapters/s3.Backend] instead of [adapters/file.Backend].
//
// Runs fully offline (no live AWS account, no moto/localstack): an
// in-process gofakes3 server (github.com/johannesboyne/gofakes3, an
// EXAMPLE-ONLY dependency never imported by adapters/s3 itself) speaks
// the real S3 HTTP API, so the real aws-sdk-go-v2 *s3.Client works
// against it unmodified by pointing its BaseEndpoint at the fake
// server's local URL.
func demoS3Backend(ctx context.Context) {
	fmt.Println("--- Backend swap: adapters/s3 (via an in-process gofakes3 server) ---")

	// 1. Start an in-process, in-memory fake S3 server.
	fakeBackend := s3mem.New()
	faker := gofakes3.New(fakeBackend)
	srv := httptest.NewServer(faker.Server())
	defer srv.Close()

	// 2. Build a REAL *s3.Client pointed at the fake server. gofakes3
	// does not validate SigV4 signatures in its default mode, so dummy
	// static credentials are sufficient.
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("DUMMYKEY", "DUMMYSECRET", "")),
	)
	if err != nil {
		log.Fatalf("demoS3Backend: LoadDefaultConfig: %v", err)
	}
	s3Client := s3sdk.NewFromConfig(cfg, func(o *s3sdk.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
		o.UsePathStyle = true // gofakes3 (like most local S3-compatible servers) needs path-style addressing.
	})

	// 3. Create the bucket (bucket lifecycle itself is out of scope for
	// api/file.Backend — a one-time setup step, exactly like a real AWS
	// deployment would do via Terraform/the console once, not per-run).
	if _, err := s3Client.CreateBucket(ctx, &s3sdk.CreateBucketInput{Bucket: aws.String(demoS3Bucket)}); err != nil {
		log.Fatalf("demoS3Backend: CreateBucket: %v", err)
	}

	// 4. The ONE line that differs from every other demo in this
	// example: adapters/s3.NewBackend instead of adapters/file.NewBackend.
	// resources.Notes itself is completely unchanged.
	s3Backend := adapters3.NewBackend(s3Client, demoS3Bucket)
	s3DemoClient := file.NewClient()
	if err := s3DemoClient.Attach(s3Backend); err != nil {
		log.Fatalf("demoS3Backend: Attach: %v", err)
	}

	vars := map[string]string{"id": "s3-note"}
	if _, err := s3DemoClient.Create(ctx, resources.Notes.WithCreate(file.Create{}), resources.Note{Title: "From S3", Body: "Same Resource declaration, different Backend."}, vars); err != nil {
		log.Fatalf("demoS3Backend: Create: %v", err)
	}
	gotAny, err := s3DemoClient.Read(ctx, resources.Notes.WithRead(file.Read{}), vars)
	if err != nil {
		log.Fatalf("demoS3Backend: Read: %v", err)
	}
	note := gotAny.(resources.Note)
	fmt.Printf("read back from S3 (fake): Title=%q Body=%q\n", note.Title, note.Body)

	// 5. S3's genuine capability upgrades over the filesystem backend,
	// demonstrated concretely: a real strong ETag from Exists/Stat
	// (never an approximation), and a native, server-side conditional
	// write via the SAME Entry.ETag -> WriteOptions.IfMatch workflow
	// every other backend already supports.
	entry, exists, err := s3DemoClient.Exists(ctx, resources.Notes.WithExists(file.Exists{}), vars)
	if err != nil {
		log.Fatalf("demoS3Backend: Exists: %v", err)
	}
	if !exists {
		log.Fatalf("demoS3Backend: want exists=true")
	}
	fmt.Printf("S3's Exists reports a REAL strong ETag (not an approximation): %s\n", entry.ETag)

	if _, err := s3DemoClient.Create(ctx, resources.Notes.WithCreate(file.Create{}), resources.Note{Title: "From S3 (updated)", Body: "Conditionally overwritten via S3's native If-Match."}, vars, file.WriteOptions{IfMatch: &entry.ETag}); err != nil {
		log.Fatalf("demoS3Backend: conditional overwrite: %v", err)
	}
	fmt.Println("conditional overwrite committed via S3's own native, server-side If-Match (no lock file, unlike the filesystem backend)")

	// 6. A streaming write, round-tripped via the SAME codec-bypassing
	// StreamWrite/StreamRead roles every other backend already
	// supports (reusing resources.Notes — Mechanism 2 is raw-byte,
	// caller-supplied content, independent of the resource's own codec
	// type) — backed here by adapters/s3's io.Pipe-to-PutObject bridge.
	streamVars := map[string]string{"id": "s3-streamed"}
	content := []byte("streamed through adapters/s3's io.Pipe bridge")
	w, err := s3DemoClient.OpenWrite(ctx, resources.Notes.WithStreamWrite(file.StreamWrite{}), streamVars)
	if err != nil {
		log.Fatalf("demoS3Backend: OpenWrite: %v", err)
	}
	if _, err := w.Write(content); err != nil {
		log.Fatalf("demoS3Backend: Write: %v", err)
	}
	if err := w.Close(); err != nil {
		log.Fatalf("demoS3Backend: Close: %v", err)
	}
	r, err := s3DemoClient.OpenRead(ctx, resources.Notes.WithStreamRead(file.StreamRead{}), streamVars)
	if err != nil {
		log.Fatalf("demoS3Backend: OpenRead: %v", err)
	}
	defer r.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		log.Fatalf("demoS3Backend: ReadFrom: %v", err)
	}
	fmt.Printf("streamed content round-tripped through S3: %q\n", buf.String())

	fmt.Println()
}
