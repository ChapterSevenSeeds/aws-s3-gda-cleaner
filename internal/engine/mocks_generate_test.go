package engine

//go:generate go run github.com/matryer/moq@v0.6.0 -pkg engine -out zz_notifier_moq_test.go ../notifier Notifier
//go:generate go run github.com/matryer/moq@v0.6.0 -pkg engine -out zz_scanner_s3client_moq_test.go ../scanner S3Client
//go:generate go run github.com/matryer/moq@v0.6.0 -pkg engine -out zz_deleter_s3deleter_moq_test.go ../deleter S3Deleter
