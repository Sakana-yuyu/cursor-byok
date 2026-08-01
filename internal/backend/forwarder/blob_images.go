package forwarder

import (
	"strings"

	"cursor/gen/agentv1"
)

// buildPrefetchedBlobMap 把 AgentRunRequest.pre_fetched_blobs 按 id → value 建 map，
// 供 selected_images 的 blob-only 图片解析使用。空 id / nil 条目跳过。
func buildPrefetchedBlobMap(blobs []*agentv1.PreFetchedBlob) map[string][]byte {
	if len(blobs) == 0 {
		return nil
	}
	m := make(map[string][]byte, len(blobs))
	for _, blob := range blobs {
		if blob == nil {
			continue
		}
		id := string(blob.GetId())
		if id == "" {
			continue
		}
		m[id] = blob.GetValue()
	}
	return m
}

// hydrateSelectedImageBlobs 把 selected_images 中「只有 blob_id、无内联 data / blob_id_with_data / path」的
// 图片，用 pre_fetched_blobs 的数据填充为内联 data。
// 修复：Cursor 粘贴图片走 blob 协议时，图片数据在 AgentRunRequest.pre_fetched_blobs，
// 若不填充，buildSelectedImageContentParts(replay.go) 会把这类图片静默丢弃，
// 图片进不了 Message.ContentParts，图片路径占位也不会触发。
func hydrateSelectedImageBlobs(userMessage *agentv1.UserMessage, blobMap map[string][]byte) {
	if userMessage == nil || userMessage.GetSelectedContext() == nil {
		return
	}
	for _, image := range userMessage.GetSelectedContext().GetSelectedImages() {
		if image == nil {
			continue
		}
		// 已有内容（内联 data / blob_id_with_data / 磁盘 path）的图片不动。
		if len(image.GetData()) > 0 || strings.TrimSpace(image.GetPath()) != "" {
			continue
		}
		if blobIDWithData := image.GetBlobIdWithData(); blobIDWithData != nil && len(blobIDWithData.GetData()) > 0 {
			continue
		}
		blobID := string(image.GetBlobId())
		if blobID == "" {
			continue
		}
		data, ok := blobMap[blobID]
		if !ok || len(data) == 0 {
			continue
		}
		image.DataOrBlobId = &agentv1.SelectedImage_Data{Data: data}
	}
}
