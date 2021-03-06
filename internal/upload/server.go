package upload

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/RexterR/stream-file/internal/pb"
	"github.com/RexterR/stream-file/pkg/config"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Repository interface {
	InsertFile(f File) error
}

// Server is the server that provides upload services
type server struct {
	pb.UnimplementedUploadServiceServer
	repo Repository
	log  *zap.Logger
}

func NewServer(repo Repository, logger *zap.Logger) *server {
	return &server{
		repo: repo,
		log:  logger,
	}
}

func (s *server) UploadFile(stream pb.UploadService_UploadFileServer) error {
	req, err := stream.Recv()
	if err != nil {
		return s.logError(status.Errorf(codes.FailedPrecondition, "cannot receive file info"))
	}

	uploadFileName := req.GetInfo().GetFileName()
	fileType := req.GetInfo().GetType()
	s.log.Info("receive an uploadFile request", zap.String("file_name", uploadFileName), zap.String("type", fileType))

	fileData := bytes.Buffer{}
	fileSize := 0

	for {
		err := s.contextError(stream.Context())
		if err != nil {
			return err
		}

		s.log.Info("waiting to receive more data")

		req, err := stream.Recv()
		if err == io.EOF {
			s.log.Warn("no more data")
			break
		}
		if err != nil {
			return s.logError(status.Errorf(codes.Unknown, "cannot receive chunk data: %v", err))
		}

		chunk := req.GetChunkData()
		size := len(chunk)

		s.log.Info("received a chunk", zap.Int("size", size))
		fileSize += size
		_, err = fileData.Write(chunk)
		if err != nil {
			return s.logError(status.Errorf(codes.DataLoss, "cannot write chunk data: %v", err))
		}
	}

	fileID, err := uuid.NewRandom()
	if err != nil {
		return s.logError(status.Errorf(codes.Internal, "cannot send response: %v", err))
	}

	fileData.Bytes()

	f := File{
		ID:        fileID.String(),
		FileName:  uploadFileName,
		FileType:  fileType,
		TotalSize: fileSize,
		File_data: fileData.Bytes(),
	}

	if err = s.repo.InsertFile(f); err != nil {
		return s.logError(status.Errorf(codes.Internal, "cannot send response: %v", err))
	}

	res := &pb.UploadResponse{
		Id:        fileID.String(),
		TotalSize: uint32(fileSize),
	}

	err = stream.SendAndClose(res)
	if err != nil {
		return s.logError(status.Errorf(codes.Unknown, "cannot send response: %v", err))
	}

	fileSizeMB := fmt.Sprintf("%.1fMB", float64(fileSize)/config.MiB1)
	s.log.Info("saved file ", zap.String("file_id", fileID.String()), zap.String("file_Size", fileSizeMB))
	return nil
}

func (s *server) contextError(ctx context.Context) error {
	switch ctx.Err() {
	case context.Canceled:
		return s.logError(status.Error(codes.Canceled, "request is canceled"))
	case context.DeadlineExceeded:
		return s.logError(status.Error(codes.DeadlineExceeded, "deadline is exceeded"))
	default:
		return nil
	}
}

func (s *server) logError(err error) error {
	if err != nil {
		s.log.Error(err.Error())
	}
	return err
}
