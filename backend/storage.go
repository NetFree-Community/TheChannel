package main

import (
	"context"
	"io"
	"log"
	"os"
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
	r2PublicURL = os.Getenv("R2_PUBLIC_URL") // optional: https://pub-xxx.r2.dev

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
func r2PresignURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	presignClient := s3.NewPresignClient(r2Client)
	req, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r2Bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

// r2Delete removes an object from R2.
func r2Delete(ctx context.Context, key string) error {
	_, err := r2Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(r2Bucket),
		Key:    aws.String(key),
	})
	return err
}
