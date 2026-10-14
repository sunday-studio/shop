package tenantapp_test

import (
	"io"
	"mime/multipart"
	"strconv"
)

func newMultipart(w io.Writer) *multipart.Writer { return multipart.NewWriter(w) }

func itoa[T ~int | ~int64](n T) string { return strconv.FormatInt(int64(n), 10) }
