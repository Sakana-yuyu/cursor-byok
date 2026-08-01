package forwarder

import (
	"testing"

	"cursor/gen/agentv1"
)

func TestBuildPrefetchedBlobMap(t *testing.T) {
	blobs := []*agentv1.PreFetchedBlob{
		{Id: []byte("b1"), Value: []byte("data1")},
		{Id: []byte("b2"), Value: []byte("data2")},
		{Id: []byte(""), Value: []byte("no-id")},
		nil,
	}
	m := buildPrefetchedBlobMap(blobs)
	if string(m["b1"]) != "data1" || string(m["b2"]) != "data2" {
		t.Errorf("map = %#v", m)
	}
	if len(m) != 2 {
		t.Errorf("应跳过空 id 和 nil 条目, len=%d", len(m))
	}
	if buildPrefetchedBlobMap(nil) != nil {
		t.Errorf("nil 输入应返回 nil")
	}
}

func imageMessage(images ...*agentv1.SelectedImage) *agentv1.UserMessage {
	return &agentv1.UserMessage{
		SelectedContext: &agentv1.SelectedContext{SelectedImages: images},
	}
}

func TestHydrateSelectedImageBlobs(t *testing.T) {
	blobMap := map[string][]byte{"b1": []byte("image-bytes")}

	t.Run("blob-only 图片被填充", func(t *testing.T) {
		img := &agentv1.SelectedImage{DataOrBlobId: &agentv1.SelectedImage_BlobId{BlobId: []byte("b1")}}
		msg := imageMessage(img)
		hydrateSelectedImageBlobs(msg, blobMap)
		if len(img.GetData()) == 0 {
			t.Fatalf("blob-only 图片应被填充 data")
		}
		if string(img.GetData()) != "image-bytes" {
			t.Errorf("data = %q, want %q", img.GetData(), "image-bytes")
		}
	})

	t.Run("已有内联 data 的图片不动", func(t *testing.T) {
		img := &agentv1.SelectedImage{DataOrBlobId: &agentv1.SelectedImage_Data{Data: []byte("existing")}}
		msg := imageMessage(img)
		hydrateSelectedImageBlobs(msg, blobMap)
		if string(img.GetData()) != "existing" {
			t.Errorf("已有 data 不应被覆盖: %q", img.GetData())
		}
	})

	t.Run("blob_id_with_data 的图片不动", func(t *testing.T) {
		img := &agentv1.SelectedImage{DataOrBlobId: &agentv1.SelectedImage_BlobIdWithData_{
			BlobIdWithData: &agentv1.SelectedImage_BlobIdWithData{BlobId: []byte("b1"), Data: []byte("bwd-data")},
		}}
		msg := imageMessage(img)
		hydrateSelectedImageBlobs(msg, blobMap)
		if len(img.GetBlobIdWithData().GetData()) == 0 {
			t.Errorf("blob_id_with_data 数据不应丢失")
		}
		if len(img.GetData()) != 0 {
			t.Errorf("blob_id_with_data 的图片不应被覆盖为 data")
		}
	})

	t.Run("有 path 的图片不动", func(t *testing.T) {
		img := &agentv1.SelectedImage{Path: "/tmp/a.png", DataOrBlobId: &agentv1.SelectedImage_BlobId{BlobId: []byte("b1")}}
		msg := imageMessage(img)
		hydrateSelectedImageBlobs(msg, blobMap)
		if len(img.GetData()) != 0 {
			t.Errorf("有 path 不应填充 data")
		}
	})

	t.Run("blob 缺失时不动", func(t *testing.T) {
		img := &agentv1.SelectedImage{DataOrBlobId: &agentv1.SelectedImage_BlobId{BlobId: []byte("missing")}}
		msg := imageMessage(img)
		hydrateSelectedImageBlobs(msg, blobMap)
		if len(img.GetData()) != 0 {
			t.Errorf("blob 缺失不应填充")
		}
	})

	t.Run("nil/空输入安全", func(t *testing.T) {
		hydrateSelectedImageBlobs(nil, blobMap)
		hydrateSelectedImageBlobs(&agentv1.UserMessage{}, blobMap)
		hydrateSelectedImageBlobs(imageMessage(&agentv1.SelectedImage{}), nil)
	})
}
