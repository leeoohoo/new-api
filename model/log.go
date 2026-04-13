package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"

	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

type Log struct {
	Id               int    `json:"id" gorm:"index:idx_created_at_id,priority:1;index:idx_user_id_id,priority:2"`
	UserId           int    `json:"user_id" gorm:"index;index:idx_user_id_id,priority:1"`
	CreatedAt        int64  `json:"created_at" gorm:"bigint;index:idx_created_at_id,priority:2;index:idx_created_at_type"`
	Type             int    `json:"type" gorm:"index:idx_created_at_type"`
	Content          string `json:"content"`
	Username         string `json:"username" gorm:"index;index:index_username_model_name,priority:2;default:''"`
	TokenName        string `json:"token_name" gorm:"index;default:''"`
	ModelName        string `json:"model_name" gorm:"index;index:index_username_model_name,priority:1;default:''"`
	Quota            int    `json:"quota" gorm:"default:0"`
	PromptTokens     int    `json:"prompt_tokens" gorm:"default:0"`
	CompletionTokens int    `json:"completion_tokens" gorm:"default:0"`
	UseTime          int    `json:"use_time" gorm:"default:0"`
	IsStream         bool   `json:"is_stream"`
	ChannelId        int    `json:"channel" gorm:"index"`
	ChannelName      string `json:"channel_name" gorm:"->"`
	TokenId          int    `json:"token_id" gorm:"default:0;index"`
	Group            string `json:"group" gorm:"index"`
	Ip               string `json:"ip" gorm:"index;default:''"`
	RequestId        string `json:"request_id,omitempty" gorm:"type:varchar(64);index:idx_logs_request_id;default:''"`
	Other            string `json:"other"`
}

// don't use iota, avoid change log type value
const (
	LogTypeUnknown = 0
	LogTypeTopup   = 1
	LogTypeConsume = 2
	LogTypeManage  = 3
	LogTypeSystem  = 4
	LogTypeError   = 5
	LogTypeRefund  = 6
)

func formatUserLogs(logs []*Log, startIdx int) {
	for i := range logs {
		logs[i].ChannelName = ""
		var otherMap map[string]interface{}
		otherMap, _ = common.StrToMap(logs[i].Other)
		if otherMap != nil {
			// Remove admin-only debug fields.
			delete(otherMap, "admin_info")
			delete(otherMap, "reject_reason")
		}
		logs[i].Other = common.MapToJsonStr(otherMap)
		logs[i].Id = startIdx + i + 1
	}
}

func GetLogByTokenId(tokenId int) (logs []*Log, err error) {
	err = LOG_DB.Model(&Log{}).Where("token_id = ?", tokenId).Order("id desc").Limit(common.MaxRecentItems).Find(&logs).Error
	formatUserLogs(logs, 0)
	return logs, err
}

func RecordLog(userId int, logType int, content string) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	err := LOG_DB.Create(log).Error
	if err != nil {
		common.SysLog("failed to record log: " + err.Error())
	}
}

func RecordErrorLog(c *gin.Context, userId int, channelId int, modelName string, tokenName string, content string, tokenId int, useTimeSeconds int,
	isStream bool, group string, other map[string]interface{}) {
	logger.LogInfo(c, fmt.Sprintf("record error log: userId=%d, channelId=%d, modelName=%s, tokenName=%s, content=%s", userId, channelId, modelName, tokenName, content))
	username := c.GetString("username")
	requestId := c.GetString(common.RequestIdKey)
	otherStr := common.MapToJsonStr(other)
	// 判断是否需要记录 IP
	needRecordIp := false
	if settingMap, err := GetUserSetting(userId, false); err == nil {
		if settingMap.RecordIpLog {
			needRecordIp = true
		}
	}
	log := &Log{
		UserId:           userId,
		Username:         username,
		CreatedAt:        common.GetTimestamp(),
		Type:             LogTypeError,
		Content:          content,
		PromptTokens:     0,
		CompletionTokens: 0,
		TokenName:        tokenName,
		ModelName:        modelName,
		Quota:            0,
		ChannelId:        channelId,
		TokenId:          tokenId,
		UseTime:          useTimeSeconds,
		IsStream:         isStream,
		Group:            group,
		Ip: func() string {
			if needRecordIp {
				return c.ClientIP()
			}
			return ""
		}(),
		RequestId: requestId,
		Other:     otherStr,
	}
	err := LOG_DB.Create(log).Error
	if err != nil {
		logger.LogError(c, "failed to record log: "+err.Error())
	}
}

type RecordConsumeLogParams struct {
	ChannelId        int                    `json:"channel_id"`
	PromptTokens     int                    `json:"prompt_tokens"`
	CompletionTokens int                    `json:"completion_tokens"`
	ModelName        string                 `json:"model_name"`
	TokenName        string                 `json:"token_name"`
	Quota            int                    `json:"quota"`
	Content          string                 `json:"content"`
	TokenId          int                    `json:"token_id"`
	UseTimeSeconds   int                    `json:"use_time_seconds"`
	IsStream         bool                   `json:"is_stream"`
	Group            string                 `json:"group"`
	Other            map[string]interface{} `json:"other"`
}

func RecordConsumeLog(c *gin.Context, userId int, params RecordConsumeLogParams) {
	if !common.LogConsumeEnabled {
		return
	}
	logger.LogInfo(c, fmt.Sprintf("record consume log: userId=%d, params=%s", userId, common.GetJsonString(params)))
	username := c.GetString("username")
	requestId := c.GetString(common.RequestIdKey)
	otherStr := common.MapToJsonStr(params.Other)
	// 判断是否需要记录 IP
	needRecordIp := false
	if settingMap, err := GetUserSetting(userId, false); err == nil {
		if settingMap.RecordIpLog {
			needRecordIp = true
		}
	}
	log := &Log{
		UserId:           userId,
		Username:         username,
		CreatedAt:        common.GetTimestamp(),
		Type:             LogTypeConsume,
		Content:          params.Content,
		PromptTokens:     params.PromptTokens,
		CompletionTokens: params.CompletionTokens,
		TokenName:        params.TokenName,
		ModelName:        params.ModelName,
		Quota:            params.Quota,
		ChannelId:        params.ChannelId,
		TokenId:          params.TokenId,
		UseTime:          params.UseTimeSeconds,
		IsStream:         params.IsStream,
		Group:            params.Group,
		Ip: func() string {
			if needRecordIp {
				return c.ClientIP()
			}
			return ""
		}(),
		RequestId: requestId,
		Other:     otherStr,
	}
	err := LOG_DB.Create(log).Error
	if err != nil {
		logger.LogError(c, "failed to record log: "+err.Error())
	}
	if common.DataExportEnabled {
		gopool.Go(func() {
			LogQuotaData(userId, username, params.ModelName, params.Quota, common.GetTimestamp(), params.PromptTokens+params.CompletionTokens)
		})
	}
}

type RecordTaskBillingLogParams struct {
	UserId    int
	LogType   int
	Content   string
	ChannelId int
	ModelName string
	Quota     int
	TokenId   int
	Group     string
	Other     map[string]interface{}
}

func RecordTaskBillingLog(params RecordTaskBillingLogParams) {
	if params.LogType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(params.UserId, false)
	tokenName := ""
	if params.TokenId > 0 {
		if token, err := GetTokenById(params.TokenId); err == nil {
			tokenName = token.Name
		}
	}
	log := &Log{
		UserId:    params.UserId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      params.LogType,
		Content:   params.Content,
		TokenName: tokenName,
		ModelName: params.ModelName,
		Quota:     params.Quota,
		ChannelId: params.ChannelId,
		TokenId:   params.TokenId,
		Group:     params.Group,
		Other:     common.MapToJsonStr(params.Other),
	}
	err := LOG_DB.Create(log).Error
	if err != nil {
		common.SysLog("failed to record task billing log: " + err.Error())
	}
}

func GetAllLogs(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, startIdx int, num int, channel int, group string, requestId string) (logs []*Log, total int64, err error) {
	var tx *gorm.DB
	if logType == LogTypeUnknown {
		tx = LOG_DB
	} else {
		tx = LOG_DB.Where("logs.type = ?", logType)
	}

	if modelName != "" {
		tx = tx.Where("logs.model_name like ?", modelName)
	}
	if username != "" {
		tx = tx.Where("logs.username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("logs.token_name = ?", tokenName)
	}
	if requestId != "" {
		tx = tx.Where("logs.request_id = ?", requestId)
	}
	if startTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", endTimestamp)
	}
	if channel != 0 {
		tx = tx.Where("logs.channel_id = ?", channel)
	}
	if group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", group)
	}
	err = tx.Model(&Log{}).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}
	err = tx.Order("logs.id desc").Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		return nil, 0, err
	}

	channelIds := types.NewSet[int]()
	for _, log := range logs {
		if log.ChannelId != 0 {
			channelIds.Add(log.ChannelId)
		}
	}

	if channelIds.Len() > 0 {
		var channels []struct {
			Id   int    `gorm:"column:id"`
			Name string `gorm:"column:name"`
		}
		if common.MemoryCacheEnabled {
			// Cache get channel
			for _, channelId := range channelIds.Items() {
				if cacheChannel, err := CacheGetChannel(channelId); err == nil {
					channels = append(channels, struct {
						Id   int    `gorm:"column:id"`
						Name string `gorm:"column:name"`
					}{
						Id:   channelId,
						Name: cacheChannel.Name,
					})
				}
			}
		} else {
			// Bulk query channels from DB
			if err = DB.Table("channels").Select("id, name").Where("id IN ?", channelIds.Items()).Find(&channels).Error; err != nil {
				return logs, total, err
			}
		}
		channelMap := make(map[int]string, len(channels))
		for _, channel := range channels {
			channelMap[channel.Id] = channel.Name
		}
		for i := range logs {
			logs[i].ChannelName = channelMap[logs[i].ChannelId]
		}
	}

	return logs, total, err
}

const logSearchCountLimit = 10000

func GetUserLogs(userId int, logType int, startTimestamp int64, endTimestamp int64, modelName string, tokenName string, startIdx int, num int, group string, requestId string) (logs []*Log, total int64, err error) {
	var tx *gorm.DB
	if logType == LogTypeUnknown {
		tx = LOG_DB.Where("logs.user_id = ?", userId)
	} else {
		tx = LOG_DB.Where("logs.user_id = ? and logs.type = ?", userId, logType)
	}

	if modelName != "" {
		modelNamePattern, err := sanitizeLikePattern(modelName)
		if err != nil {
			return nil, 0, err
		}
		tx = tx.Where("logs.model_name LIKE ? ESCAPE '!'", modelNamePattern)
	}
	if tokenName != "" {
		tx = tx.Where("logs.token_name = ?", tokenName)
	}
	if requestId != "" {
		tx = tx.Where("logs.request_id = ?", requestId)
	}
	if startTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", endTimestamp)
	}
	if group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", group)
	}
	err = tx.Model(&Log{}).Limit(logSearchCountLimit).Count(&total).Error
	if err != nil {
		common.SysError("failed to count user logs: " + err.Error())
		return nil, 0, errors.New("查询日志失败")
	}
	err = tx.Order("logs.id desc").Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		common.SysError("failed to search user logs: " + err.Error())
		return nil, 0, errors.New("查询日志失败")
	}

	formatUserLogs(logs, startIdx)
	return logs, total, err
}

type Stat struct {
	Quota int `json:"quota"`
	Rpm   int `json:"rpm"`
	Tpm   int `json:"tpm"`
}

type AdminUsageReportFilter struct {
	StartTimestamp int64
	EndTimestamp   int64
	ModelName      string
	Channel        int
	Group          string
}

type AdminUsageTrendItem struct {
	Timestamp    int64   `json:"timestamp" gorm:"column:timestamp"`
	RequestCount int64   `json:"request_count" gorm:"column:request_count"`
	SuccessCount int64   `json:"success_count" gorm:"column:success_count"`
	ErrorCount   int64   `json:"error_count" gorm:"column:error_count"`
	Quota        int64   `json:"quota" gorm:"column:quota"`
	TokenUsed    int64   `json:"token_used" gorm:"column:token_used"`
	SuccessRate  float64 `json:"success_rate" gorm:"-"`
	ErrorRate    float64 `json:"error_rate" gorm:"-"`
}

type AdminUsageModelItem struct {
	ModelName    string  `json:"model_name" gorm:"column:model_name"`
	RequestCount int64   `json:"request_count" gorm:"column:request_count"`
	SuccessCount int64   `json:"success_count" gorm:"column:success_count"`
	ErrorCount   int64   `json:"error_count" gorm:"column:error_count"`
	Quota        int64   `json:"quota" gorm:"column:quota"`
	TokenUsed    int64   `json:"token_used" gorm:"column:token_used"`
	SuccessRate  float64 `json:"success_rate" gorm:"-"`
	ErrorRate    float64 `json:"error_rate" gorm:"-"`
}

type AdminUsageChannelItem struct {
	ChannelId    int     `json:"channel_id" gorm:"column:channel_id"`
	ChannelName  string  `json:"channel_name" gorm:"-"`
	RequestCount int64   `json:"request_count" gorm:"column:request_count"`
	SuccessCount int64   `json:"success_count" gorm:"column:success_count"`
	ErrorCount   int64   `json:"error_count" gorm:"column:error_count"`
	Quota        int64   `json:"quota" gorm:"column:quota"`
	TokenUsed    int64   `json:"token_used" gorm:"column:token_used"`
	SuccessRate  float64 `json:"success_rate" gorm:"-"`
	ErrorRate    float64 `json:"error_rate" gorm:"-"`
}

type AdminUsageUserItem struct {
	UserId       int     `json:"user_id" gorm:"column:user_id"`
	Username     string  `json:"username" gorm:"column:username"`
	RequestCount int64   `json:"request_count" gorm:"column:request_count"`
	SuccessCount int64   `json:"success_count" gorm:"column:success_count"`
	ErrorCount   int64   `json:"error_count" gorm:"column:error_count"`
	Quota        int64   `json:"quota" gorm:"column:quota"`
	TokenUsed    int64   `json:"token_used" gorm:"column:token_used"`
	SuccessRate  float64 `json:"success_rate" gorm:"-"`
	ErrorRate    float64 `json:"error_rate" gorm:"-"`
}

type AdminUsageUserQuery struct {
	Filter     AdminUsageReportFilter
	UserKeyword string
	StartIdx   int
	Num        int
	SortBy     string
	SortOrder  string
}

func buildAdminUsageReportBaseQuery(filter AdminUsageReportFilter) (*gorm.DB, error) {
	tx := LOG_DB.Model(&Log{}).Where("logs.type IN ?", []int{LogTypeConsume, LogTypeError})
	if filter.StartTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", filter.StartTimestamp)
	}
	if filter.EndTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", filter.EndTimestamp)
	}
	if filter.ModelName != "" {
		modelNamePattern, err := sanitizeLikePattern(filter.ModelName)
		if err != nil {
			return nil, err
		}
		tx = tx.Where("logs.model_name LIKE ? ESCAPE '!'", modelNamePattern)
	}
	if filter.Channel != 0 {
		tx = tx.Where("logs.channel_id = ?", filter.Channel)
	}
	if filter.Group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", filter.Group)
	}
	return tx, nil
}

func calcUsageRates(requestCount int64, successCount int64, errorCount int64) (float64, float64) {
	if requestCount <= 0 {
		return 0, 0
	}
	base := float64(requestCount)
	return float64(successCount) * 100 / base, float64(errorCount) * 100 / base
}

func GetAdminUsageTrend(filter AdminUsageReportFilter, bucketSeconds int64) (items []*AdminUsageTrendItem, err error) {
	if bucketSeconds <= 0 {
		return nil, errors.New("无效的时间粒度")
	}
	tx, err := buildAdminUsageReportBaseQuery(filter)
	if err != nil {
		return nil, err
	}

	type trendRaw struct {
		CreatedAt        int64 `gorm:"column:created_at"`
		Type             int   `gorm:"column:type"`
		Quota            int   `gorm:"column:quota"`
		PromptTokens     int   `gorm:"column:prompt_tokens"`
		CompletionTokens int   `gorm:"column:completion_tokens"`
	}

	var rows []*trendRaw
	err = tx.Select("logs.created_at, logs.type, logs.quota, logs.prompt_tokens, logs.completion_tokens").Order("logs.created_at asc").Find(&rows).Error
	if err != nil {
		common.SysError("failed to query usage trend: " + err.Error())
		return nil, errors.New("查询使用趋势失败")
	}

	if filter.StartTimestamp == 0 || filter.EndTimestamp == 0 || filter.EndTimestamp < filter.StartTimestamp {
		return make([]*AdminUsageTrendItem, 0), nil
	}

	alignedStart := filter.StartTimestamp - filter.StartTimestamp%bucketSeconds
	alignedEnd := filter.EndTimestamp - filter.EndTimestamp%bucketSeconds
	if alignedEnd < alignedStart {
		alignedEnd = alignedStart
	}

	bucketMap := make(map[int64]*AdminUsageTrendItem, len(rows))
	for _, row := range rows {
		bucketStart := row.CreatedAt - row.CreatedAt%bucketSeconds
		item, ok := bucketMap[bucketStart]
		if !ok {
			item = &AdminUsageTrendItem{Timestamp: bucketStart}
			bucketMap[bucketStart] = item
		}
		item.RequestCount++
		if row.Type == LogTypeConsume {
			item.SuccessCount++
		} else if row.Type == LogTypeError {
			item.ErrorCount++
		}
		item.Quota += int64(row.Quota)
		item.TokenUsed += int64(row.PromptTokens + row.CompletionTokens)
	}

	capacity := int((alignedEnd-alignedStart)/bucketSeconds) + 1
	items = make([]*AdminUsageTrendItem, 0, capacity)
	for ts := alignedStart; ts <= alignedEnd; ts += bucketSeconds {
		item, ok := bucketMap[ts]
		if !ok {
			item = &AdminUsageTrendItem{Timestamp: ts}
		}
		item.SuccessRate, item.ErrorRate = calcUsageRates(item.RequestCount, item.SuccessCount, item.ErrorCount)
		items = append(items, item)
	}

	return items, nil
}

func GetAdminUsageByModel(filter AdminUsageReportFilter, limit int) (items []*AdminUsageModelItem, err error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	tx, err := buildAdminUsageReportBaseQuery(filter)
	if err != nil {
		return nil, err
	}

	err = tx.Select(
		"logs.model_name as model_name, count(*) as request_count, "+
			"sum(case when logs.type = ? then 1 else 0 end) as success_count, "+
			"sum(case when logs.type = ? then 1 else 0 end) as error_count, "+
			"coalesce(sum(logs.quota), 0) as quota, "+
			"coalesce(sum(logs.prompt_tokens + logs.completion_tokens), 0) as token_used",
		LogTypeConsume, LogTypeError,
	).Group("logs.model_name").Order("request_count desc").Limit(limit).Scan(&items).Error
	if err != nil {
		common.SysError("failed to query usage by model: " + err.Error())
		return nil, errors.New("查询模型维度统计失败")
	}

	for _, item := range items {
		item.SuccessRate, item.ErrorRate = calcUsageRates(item.RequestCount, item.SuccessCount, item.ErrorCount)
	}
	return items, nil
}

func fillUsageChannelNames(items []*AdminUsageChannelItem) error {
	if len(items) == 0 {
		return nil
	}

	channelIds := make([]int, 0, len(items))
	seen := make(map[int]struct{}, len(items))
	for _, item := range items {
		if item.ChannelId <= 0 {
			continue
		}
		if _, ok := seen[item.ChannelId]; ok {
			continue
		}
		seen[item.ChannelId] = struct{}{}
		channelIds = append(channelIds, item.ChannelId)
	}
	if len(channelIds) == 0 {
		return nil
	}

	var channels []struct {
		Id   int    `gorm:"column:id"`
		Name string `gorm:"column:name"`
	}
	if err := DB.Table("channels").Select("id, name").Where("id IN ?", channelIds).Find(&channels).Error; err != nil {
		common.SysError("failed to query channels for usage report: " + err.Error())
		return errors.New("查询渠道信息失败")
	}

	channelMap := make(map[int]string, len(channels))
	for _, channel := range channels {
		channelMap[channel.Id] = channel.Name
	}
	for i := range items {
		items[i].ChannelName = channelMap[items[i].ChannelId]
	}
	return nil
}

func GetAdminUsageByChannel(filter AdminUsageReportFilter, limit int) (items []*AdminUsageChannelItem, err error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	tx, err := buildAdminUsageReportBaseQuery(filter)
	if err != nil {
		return nil, err
	}

	err = tx.Select(
		"logs.channel_id as channel_id, count(*) as request_count, "+
			"sum(case when logs.type = ? then 1 else 0 end) as success_count, "+
			"sum(case when logs.type = ? then 1 else 0 end) as error_count, "+
			"coalesce(sum(logs.quota), 0) as quota, "+
			"coalesce(sum(logs.prompt_tokens + logs.completion_tokens), 0) as token_used",
		LogTypeConsume, LogTypeError,
	).Group("logs.channel_id").Order("request_count desc").Limit(limit).Scan(&items).Error
	if err != nil {
		common.SysError("failed to query usage by channel: " + err.Error())
		return nil, errors.New("查询渠道维度统计失败")
	}

	if err = fillUsageChannelNames(items); err != nil {
		return nil, err
	}
	for _, item := range items {
		item.SuccessRate, item.ErrorRate = calcUsageRates(item.RequestCount, item.SuccessCount, item.ErrorCount)
	}
	return items, nil
}

func normalizeUsageSortOrder(sortOrder string) string {
	if strings.EqualFold(strings.TrimSpace(sortOrder), "asc") {
		return "asc"
	}
	return "desc"
}

func getAdminUsageUserSortField(sortBy string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(sortBy)) {
	case "", "request_count":
		return "request_count", true
	case "quota":
		return "quota", true
	case "token_used":
		return "token_used", true
	case "success_count":
		return "success_count", true
	case "error_count":
		return "error_count", true
	case "user_id":
		return "logs.user_id", true
	case "username":
		return "logs.username", true
	default:
		return "", false
	}
}

func GetAdminUsageByUser(query AdminUsageUserQuery) (items []*AdminUsageUserItem, total int64, err error) {
	sortField, ok := getAdminUsageUserSortField(query.SortBy)
	if !ok {
		return nil, 0, errors.New("不支持的排序字段")
	}
	if query.StartIdx < 0 {
		query.StartIdx = 0
	}
	if query.Num <= 0 {
		query.Num = common.ItemsPerPage
	}
	if query.Num > 100 {
		query.Num = 100
	}

	tx, err := buildAdminUsageReportBaseQuery(query.Filter)
	if err != nil {
		return nil, 0, err
	}
	if query.UserKeyword != "" {
		userPattern, err := sanitizeLikePattern(query.UserKeyword)
		if err != nil {
			return nil, 0, err
		}
		tx = tx.Where("logs.username LIKE ? ESCAPE '!'", userPattern)
	}

	countTx := tx.Session(&gorm.Session{}).Select("logs.user_id, logs.username").Group("logs.user_id, logs.username")
	err = LOG_DB.Table("(?) as usage_user_groups", countTx).Count(&total).Error
	if err != nil {
		common.SysError("failed to count usage users: " + err.Error())
		return nil, 0, errors.New("查询用户维度统计失败")
	}

	orderClause := sortField + " " + normalizeUsageSortOrder(query.SortOrder)
	err = tx.Session(&gorm.Session{}).Select(
		"logs.user_id as user_id, logs.username as username, count(*) as request_count, "+
			"sum(case when logs.type = ? then 1 else 0 end) as success_count, "+
			"sum(case when logs.type = ? then 1 else 0 end) as error_count, "+
			"coalesce(sum(logs.quota), 0) as quota, "+
			"coalesce(sum(logs.prompt_tokens + logs.completion_tokens), 0) as token_used",
		LogTypeConsume, LogTypeError,
	).Group("logs.user_id, logs.username").Order(orderClause).Limit(query.Num).Offset(query.StartIdx).Scan(&items).Error
	if err != nil {
		common.SysError("failed to query usage users: " + err.Error())
		return nil, 0, errors.New("查询用户维度统计失败")
	}

	for _, item := range items {
		item.SuccessRate, item.ErrorRate = calcUsageRates(item.RequestCount, item.SuccessCount, item.ErrorCount)
	}
	return items, total, nil
}

func SumUsedQuota(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, channel int, group string) (stat Stat, err error) {
	tx := LOG_DB.Table("logs").Select("sum(quota) quota")

	// 为rpm和tpm创建单独的查询
	rpmTpmQuery := LOG_DB.Table("logs").Select("count(*) rpm, sum(prompt_tokens) + sum(completion_tokens) tpm")

	if username != "" {
		tx = tx.Where("username = ?", username)
		rpmTpmQuery = rpmTpmQuery.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
		rpmTpmQuery = rpmTpmQuery.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		modelNamePattern, err := sanitizeLikePattern(modelName)
		if err != nil {
			return stat, err
		}
		tx = tx.Where("model_name LIKE ? ESCAPE '!'", modelNamePattern)
		rpmTpmQuery = rpmTpmQuery.Where("model_name LIKE ? ESCAPE '!'", modelNamePattern)
	}
	if channel != 0 {
		tx = tx.Where("channel_id = ?", channel)
		rpmTpmQuery = rpmTpmQuery.Where("channel_id = ?", channel)
	}
	if group != "" {
		tx = tx.Where(logGroupCol+" = ?", group)
		rpmTpmQuery = rpmTpmQuery.Where(logGroupCol+" = ?", group)
	}

	tx = tx.Where("type = ?", LogTypeConsume)
	rpmTpmQuery = rpmTpmQuery.Where("type = ?", LogTypeConsume)

	// 只统计最近60秒的rpm和tpm
	rpmTpmQuery = rpmTpmQuery.Where("created_at >= ?", time.Now().Add(-60*time.Second).Unix())

	// 执行查询
	if err := tx.Scan(&stat).Error; err != nil {
		common.SysError("failed to query log stat: " + err.Error())
		return stat, errors.New("查询统计数据失败")
	}
	if err := rpmTpmQuery.Scan(&stat).Error; err != nil {
		common.SysError("failed to query rpm/tpm stat: " + err.Error())
		return stat, errors.New("查询统计数据失败")
	}

	return stat, nil
}

func SumUsedToken(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string) (token int) {
	tx := LOG_DB.Table("logs").Select("ifnull(sum(prompt_tokens),0) + ifnull(sum(completion_tokens),0)")
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	tx.Where("type = ?", LogTypeConsume).Scan(&token)
	return token
}

func DeleteOldLog(ctx context.Context, targetTimestamp int64, limit int) (int64, error) {
	var total int64 = 0

	for {
		if nil != ctx.Err() {
			return total, ctx.Err()
		}

		result := LOG_DB.Where("created_at < ?", targetTimestamp).Limit(limit).Delete(&Log{})
		if nil != result.Error {
			return total, result.Error
		}

		total += result.RowsAffected

		if result.RowsAffected < int64(limit) {
			break
		}
	}

	return total, nil
}
