package azfile

import (
	"context"
	"encoding/base64"
	"io"
	"strconv"

	"github.com/Azure/azure-storage-file-go/azfile"

	"github.com/beyondstorage/go-storage/v4/pkg/iowrap"
	. "github.com/beyondstorage/go-storage/v4/types"
)

func (s *Storage) create(path string, opt pairStorageCreate) (o *Object) {
	rp := s.getAbsPath(path)

	if opt.HasObjectMode && opt.ObjectMode.IsDir() {
		o = s.newObject(true)
		o.Mode |= ModeDir
	} else {
		o = s.newObject(false)
		o.Mode |= ModeRead
	}

	o.ID = rp
	o.Path = path

	return o
}

func (s *Storage) createDir(ctx context.Context, path string, opt pairStorageCreateDir) (o *Object, err error) {
	rp := s.getAbsPath(path)

	attribute := azfile.FileAttributeNone

	properties := azfile.SMBProperties{
		FileAttributes: &attribute,
	}

	fi, err := s.client.NewDirectoryURL(path).GetProperties(ctx)
	if err == nil {
		// The directory exist, we should set the metadata.
		o = s.newObject(true)
		o.SetLastModified(fi.LastModified())
	} else if !checkError(err, fileNotFound) {
		// Something error other then file not found happened, return directly.
		return nil, err
	} else {
		// The directory not exists, we should create the directory.
		_, err = s.client.NewDirectoryURL(path).Create(ctx, nil, properties)
		if err != nil {
			return nil, err
		}

		o = s.newObject(false)
	}

	o.ID = rp
	o.Path = path
	o.Mode |= ModeDir

	return
}

func (s *Storage) delete(ctx context.Context, path string, opt pairStorageDelete) (err error) {
	if opt.HasObjectMode && opt.ObjectMode.IsDir() {
		_, err = s.client.NewDirectoryURL(path).Delete(ctx)
	} else {
		_, err = s.client.NewFileURL(path).Delete(ctx)
	}

	if err != nil {
		// azfile Delete is not idempotent, so we need to check file not found error.
		//
		// References
		// - [GSP-46](https://github.com/beyondstorage/specs/blob/master/rfcs/46-idempotent-delete.md)
		// - https://docs.microsoft.com/en-us/rest/api/storageservices/delete-file2#remarks
		if checkError(err, fileNotFound) {
			err = nil
		} else {
			return err
		}
	}

	return nil
}

func (s *Storage) list(ctx context.Context, path string, opt pairStorageList) (oi *ObjectIterator, err error) {
	input := &objectPageStatus{
		maxResults: 200,
		prefix:     s.getAbsPath(path),
	}

	return NewObjectIterator(ctx, s.nextObjectPage, input), nil
}

func (s *Storage) metadata(opt pairStorageMetadata) (meta *StorageMeta) {
	meta = NewStorageMeta()
	meta.WorkDir = s.workDir
	return meta
}

func (s *Storage) nextObjectPage(ctx context.Context, page *ObjectPage) error {
	input := page.Status.(*objectPageStatus)

	options := azfile.ListFilesAndDirectoriesOptions{
		Prefix:     input.prefix,
		MaxResults: input.maxResults,
	}

	output, err := s.client.ListFilesAndDirectoriesSegment(ctx, input.marker, options)
	if err != nil {
		return err
	}

	for _, v := range output.DirectoryItems {
		o, err := s.formatDirObject(v)
		if err != nil {
			return err
		}

		page.Data = append(page.Data, o)
	}

	for _, v := range output.FileItems {
		o, err := s.formatFileObject(v)
		if err != nil {
			return err
		}

		page.Data = append(page.Data, o)
	}

	if !output.NextMarker.NotDone() {
		return IterateDone
	}

	input.marker = output.NextMarker

	return nil
}

func (s *Storage) read(ctx context.Context, path string, w io.Writer, opt pairStorageRead) (n int64, err error) {
	offset := int64(0)
	if opt.HasOffset {
		offset = opt.Offset
	}

	count := int64(azfile.CountToEnd)
	if opt.HasSize {
		count = opt.Size
	}

	output, err := s.client.NewFileURL(path).Download(ctx, offset, count, false)
	if err != nil {
		return 0, err
	}
	defer func() {
		cErr := output.Response().Body.Close()
		if cErr != nil {
			err = cErr
		}
	}()

	rc := output.Response().Body
	if opt.HasIoCallback {
		rc = iowrap.CallbackReadCloser(rc, opt.IoCallback)
	}

	return io.Copy(w, rc)
}

func (s *Storage) stat(ctx context.Context, path string, opt pairStorageStat) (o *Object, err error) {
	rp := s.getAbsPath(path)

	var dirOutput *azfile.DirectoryGetPropertiesResponse
	var fileOutput *azfile.FileGetPropertiesResponse

	if opt.HasObjectMode && opt.ObjectMode.IsDir() {
		dirOutput, err = s.client.NewDirectoryURL(path).GetProperties(ctx)
	} else {
		fileOutput, err = s.client.NewFileURL(path).GetProperties(ctx)
	}

	if err != nil {
		return nil, err
	}

	o = s.newObject(true)
	o.ID = rp
	o.Path = path

	if opt.HasObjectMode && opt.ObjectMode.IsDir() {
		o.Mode |= ModeDir

		o.SetLastModified(dirOutput.LastModified())

		if v := string(dirOutput.ETag()); v != "" {
			o.SetEtag(v)
		}

		var sm ObjectSystemMetadata
		if v, err := strconv.ParseBool(dirOutput.IsServerEncrypted()); err == nil {
			sm.ServerEncrypted = v
		}
		o.SetSystemMetadata(sm)
	} else {
		o.Mode |= ModeRead

		o.SetContentLength(fileOutput.ContentLength())
		o.SetLastModified(fileOutput.LastModified())

		if v := string(fileOutput.ETag()); v != "" {
			o.SetEtag(v)
		}
		if v := fileOutput.ContentType(); v != "" {
			o.SetContentType(v)
		}
		if v := fileOutput.ContentMD5(); len(v) > 0 {
			o.SetContentMd5(base64.StdEncoding.EncodeToString(v))
		}

		var sm ObjectSystemMetadata
		if v, err := strconv.ParseBool(fileOutput.IsServerEncrypted()); err == nil {
			sm.ServerEncrypted = v
		}
		o.SetSystemMetadata(sm)
	}

	return o, nil
}

func (s *Storage) write(ctx context.Context, path string, r io.Reader, size int64, opt pairStorageWrite) (n int64, err error) {
	if opt.HasIoCallback {
		r = iowrap.CallbackReader(r, opt.IoCallback)
	}

	headers := azfile.FileHTTPHeaders{}

	if opt.HasContentType {
		headers.ContentType = opt.ContentType
	}

	// `Create` only initializes the file.
	// ref: https://docs.microsoft.com/en-us/rest/api/storageservices/create-file
	_, err = s.client.NewFileURL(path).Create(ctx, size, headers, nil)
	if err != nil {
		return 0, err
	}

	body := iowrap.SizedReadSeekCloser(r, size)

	var transactionalMD5 []byte
	if opt.HasContentMd5 {
		transactionalMD5, err = base64.StdEncoding.DecodeString(opt.ContentMd5)
		if err != nil {
			return 0, err
		}
	}

	// Since `Create' only initializes the file, we need to call `UploadRange' to write the contents to the file.
	_, err = s.client.NewFileURL(path).UploadRange(ctx, 0, body, transactionalMD5)
	if err != nil {
		return 0, err
	}

	return size, nil
}
