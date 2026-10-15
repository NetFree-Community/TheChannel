package main

import (
	"context"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

var r2Client *s3.Client
var r2Bucket string
var r2PublicURL string
var r2Enabled bool

func initR2() {
	accountID := os.Getenv("R2_ACCOUNT_ID")
	accessKey := os.Getenv("R2_ACCESS_KEY_ID")
	secretKey := os.Getenv("R2_SECRET_ACCESS_KEY")
	r2Bucket = os.Getenv("R2_BUCKET_NAME")
	// Optional: https://pub-xxx.r2.dev. Trailing slashes are dropped because
	// the key is appended with its own slash, and a doubled one addresses a
	// different (leading-slash) object on S3-style stores. The sample.env
	// placeholder is refused outright: copied as-is it redirected every file
	// to a host that does not exist, with nothing in the logs.
	r2PublicURL = strings.TrimRight(strings.TrimSpace(os.Getenv("R2_PUBLIC_URL")), "/")
	if strings.Contains(r2PublicURL, "pub-xxxx") {
		log.Printf("R2_PUBLIC_URL %q looks like the sample placeholder; ignoring it and using pre-signed URLs\n", r2PublicURL)
		r2PublicURL = ""
	}

	// sample.env shipped these as "your_..." placeholders; copied as-is they
	// pass the non-empty test below, R2 comes up against a host that does not
	// exist and every upload fails with an opaque 500.
	for _, v := range []string{accountID, accessKey, secretKey, r2Bucket} {
		if strings.HasPrefix(v, "your_") {
			log.Println("WARNING: R2_* still holds a sample.env placeholder; R2 disabled, using local file storage")
			return
		}
	}

	if accountID == "" || accessKey == "" || secretKey == "" || r2Bucket == "" {
		log.Println("R2 not configured, using local file storage")
		return
	}

	endpoint := "https://" + accountID + ".r2.cloudflarestorage.com"

	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
		config.WithRegion("auto"),
	)
	if err != nil {
		log.Printf("R2 config error: %v — falling back to local storage\n", err)
		return
	}

	r2Client = s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})

	r2Enabled = true
	log.Printf("R2 storage enabled (bucket: %s)\n", r2Bucket)
}

// r2ObjectKey returns the R2 object key for a file hash.
func r2ObjectKey(hash string) string {
	return "files/" + hash[:2] + "/" + hash[2:4] + "/" + hash
}

// r2Upload uploads a file to R2. Returns an error if failed.
func r2Upload(ctx context.Context, key string, body io.Reader, contentType string) error {
	_, err := r2Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(r2Bucket),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType),
	})
	return err
}

// r2Exists checks if an object already exists in R2 (deduplication).
func r2Exists(ctx context.Context, key string) bool {
	_, err := r2Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(r2Bucket),
		Key:    aws.String(key),
	})
	return err == nil
}

// r2Download downloads an object from R2 and returns its body.
// Used as a fallback when neither public URL nor pre-signed URL is available.
func r2Download(ctx context.Context, key string) (io.ReadCloser, *string, error) {
	result, err := r2Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r2Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, nil, err
	}
	return result.Body, result.ContentType, nil
}

// r2PresignURL generates a short-lived pre-signed URL for a private R2 object.
// The client fetches the file directly from R2, bypassing the backend entirely.
// r2PresignURL signs a GET for key. disposition, when set, is baked into the
// signed URL as the Content-Disposition R2 will answer with: the header the
// backend sets on its own 302 is discarded by the browser along with the
// redirect, so without this a private-bucket download was saved under the
// object's hash with no extension.
func r2PresignURL(ctx context.Context, key string, ttl time.Duration, disposition string) (string, error) {
	presignClient := s3.NewPresignClient(r2Client)
	in := &s3.GetObjectInput{
		Bucket: aws.String(r2Bucket),
		Key:    aws.String(key),
	}
	if disposition != "" {
		in.ResponseContentDisposition = aws.String(disposition)
	}
	// R2 sends no caching header of its own; without one the browser would
	// fetch the same signed URL again instead of using its copy.
	in.ResponseCacheControl = aws.String("private, max-age=3600")
	req, err := presignClient.PresignGetObject(ctx, in, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

// r2Delete removes an object from R2.
func r2Delete(ctx context.Context, key string) error {
	r2ForgetKey(key)
	_, err := r2Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(r2Bucket),
		Key:    aws.String(key),
	})
	return err
}
