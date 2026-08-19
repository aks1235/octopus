package task

import (
	"context"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/utils/xstrings"
	"github.com/charmbracelet/log"
)

// GroupItemReconcileTask 周期对账:扫描全部 GroupItem,清理两类孤儿引用——
// (1)渠道已删除(channel_id 在渠道表不存在);
// (2)模型已下架(model_name 不在该渠道的 Model+CustomModel 列表)。
// 作为 AutoSync=false 手改模型等遗漏路径的统一兜底。不禁用渠道(① 置 Enabled=false 不等于删除)。
func GroupItemReconcileTask() {
	log.Debugf("group item reconcile task started")
	startTime := time.Now()
	defer func() {
		log.Debugf("group item reconcile task finished, cost: %s", time.Since(startTime))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	items, err := op.GroupItemListAll(ctx)
	if err != nil {
		log.Errorf("failed to list all group items: %v", err)
		return
	}
	if len(items) == 0 {
		log.Debugf("reconcile: no group items to scan")
		return
	}

	channels, err := op.ChannelList(ctx)
	if err != nil {
		log.Errorf("failed to list channels for reconcile: %v", err)
		return
	}

	// 渠道存在集合 + 渠道→模型名集合
	existChID := make(map[int]struct{}, len(channels))
	chModelSet := make(map[int]map[string]struct{}, len(channels))
	for _, ch := range channels {
		existChID[ch.ID] = struct{}{}
		set := make(map[string]struct{})
		for _, m := range xstrings.SplitTrimCompact(",", ch.Model, ch.CustomModel) {
			set[m] = struct{}{}
		}
		chModelSet[ch.ID] = set
	}

	// 收集孤儿:渠道已删 或 模型已下架
	affectedGroupIDs := make(map[int]struct{})
	toDel := make([]model.GroupIDAndLLMName, 0)
	for _, item := range items {
		if _, ok := existChID[item.ChannelID]; !ok {
			// 渠道已删除
			toDel = append(toDel, model.GroupIDAndLLMName{ChannelID: item.ChannelID, ModelName: item.ModelName})
			affectedGroupIDs[item.GroupID] = struct{}{}
			continue
		}
		set, ok := chModelSet[item.ChannelID]
		if !ok {
			// 渠道存在但无模型集合(理论上不发生,兜底按孤儿处理)
			toDel = append(toDel, model.GroupIDAndLLMName{ChannelID: item.ChannelID, ModelName: item.ModelName})
			affectedGroupIDs[item.GroupID] = struct{}{}
			continue
		}
		if _, has := set[item.ModelName]; !has {
			// 模型已下架
			toDel = append(toDel, model.GroupIDAndLLMName{ChannelID: item.ChannelID, ModelName: item.ModelName})
			affectedGroupIDs[item.GroupID] = struct{}{}
		}
	}

	if len(toDel) == 0 {
		log.Debugf("reconcile: no orphan group items (%d scanned)", len(items))
		return
	}

	if err := op.GroupItemBatchDelByChannelAndModels(toDel, ctx); err != nil {
		// GroupItemBatchDelByChannelAndModels 内部已刷新受影响 group 缓存,此处仅记错误
		log.Errorf("failed to delete orphan group items: %v", err)
		return
	}

	groupIDs := make([]int, 0, len(affectedGroupIDs))
	for gid := range affectedGroupIDs {
		groupIDs = append(groupIDs, gid)
	}
	log.Infof("reconcile: deleted %d orphan group items across %d groups", len(toDel), len(groupIDs))
}
