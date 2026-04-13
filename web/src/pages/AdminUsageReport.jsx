/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { API, renderNumber, renderQuota, showError } from '../helpers';
import {
  Banner,
  Button,
  Card,
  Empty,
  Input,
  Pagination,
  Select,
  Spin,
  Switch,
  Table,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { VChart } from '@visactor/react-vchart';
import { CHART_CONFIG } from '../constants/dashboard.constants';
import './AdminUsageReport.css';

const DEFAULT_PAGE_SIZE = 20;
const FULL_WIDTH_STYLE = { width: '100%' };
const FULL_CHART_STYLE = { width: '100%', height: '100%' };

const QUICK_RANGES = [
  { key: '24h', days: 1, label: '最近24小时' },
  { key: '7d', days: 7, label: '最近7天' },
  { key: '30d', days: 30, label: '最近30天' },
];

const BUCKET_OPTIONS = [
  { label: '1小时', value: 3600 },
  { label: '6小时', value: 21600 },
  { label: '1天', value: 86400 },
];

const MODEL_TOP_N_OPTIONS = [
  { label: 'Top 5', value: 5 },
  { label: 'Top 8', value: 8 },
  { label: 'Top 10', value: 10 },
  { label: 'Top 12', value: 12 },
];

const MODEL_SORT_FIELD_OPTIONS = [
  { label: '按 Token', value: 'token_used' },
  { label: '按请求数', value: 'request_count' },
  { label: '按配额', value: 'quota' },
];

const MODEL_LABEL_MAX_LENGTH = 18;

const toNumber = (value) => {
  const num = Number(value);
  return Number.isFinite(num) ? num : 0;
};

const normalizeModelName = (value) => {
  const text = typeof value === 'string' ? value.trim() : '';
  return text || '未命名模型';
};

const normalizeChannelName = (channelId, channelName) => {
  const name = typeof channelName === 'string' ? channelName.trim() : '';
  if (name) {
    return name;
  }
  if (toNumber(channelId) > 0) {
    return `#${channelId}`;
  }
  return '未分配渠道';
};

const unwrapData = (response) => response?.data?.data || {};

const getRangeByDays = (days) => {
  const end = Math.floor(Date.now() / 1000);
  return {
    start: end - days * 24 * 3600,
    end,
  };
};

const getPreviousRange = (startTimestamp, endTimestamp) => {
  const start = Math.max(1, toNumber(startTimestamp));
  const end = Math.max(start, toNumber(endTimestamp));
  const span = Math.max(1, end - start + 1);
  const previousEnd = Math.max(1, start - 1);
  return {
    start: Math.max(1, previousEnd - span + 1),
    end: previousEnd,
  };
};

const getRecommendedBucketSeconds = (days) => {
  if (days <= 7) {
    return 3600;
  }
  if (days <= 30) {
    return 21600;
  }
  return 86400;
};

const formatTimestamp = (timestamp) => {
  if (!timestamp) {
    return '-';
  }
  return new Date(toNumber(timestamp) * 1000).toLocaleString();
};

const formatBucketLabel = (timestamp, bucketSeconds) => {
  const date = new Date(toNumber(timestamp) * 1000);
  if (bucketSeconds >= 86400) {
    return date.toLocaleDateString();
  }
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  const hours = String(date.getHours()).padStart(2, '0');
  const minutes = String(date.getMinutes()).padStart(2, '0');
  return `${month}-${day} ${hours}:${minutes}`;
};

const trimToFilterValue = (value) => {
  if (typeof value !== 'string') {
    return '';
  }
  return value.trim();
};

const formatRate = (value) => `${toNumber(value).toFixed(2)}%`;

const getAxisModelLabel = (value) => {
  const text = String(value || '');
  if (text.length <= MODEL_LABEL_MAX_LENGTH) {
    return text;
  }
  return `${text.slice(0, MODEL_LABEL_MAX_LENGTH)}...`;
};

const getSortMetricValue = (item, metric) => {
  if (!item || !metric) {
    return 0;
  }
  return toNumber(item[metric]);
};

const MetricCard = ({ title, value, hint = '', tagColor = 'blue' }) => (
  <div className='rounded-md border border-[var(--semi-color-border)] bg-[var(--semi-color-bg-0)] p-3'>
    <div className='flex items-center justify-between'>
      <Typography.Text type='tertiary' size='small'>
        {title}
      </Typography.Text>
      {hint ? <Tag color={tagColor}>{hint}</Tag> : null}
    </div>
    <Typography.Title heading={4} style={{ margin: '8px 0 0' }}>
      {value}
    </Typography.Title>
  </div>
);

const ChartBox = ({ spec, height = 360 }) => (
  <div className='admin-usage-report__chart-box' style={{ height }}>
    <VChart spec={spec} option={CHART_CONFIG} style={FULL_CHART_STYLE} />
  </div>
);

const AdminUsageReport = () => {
  const { t } = useTranslation();

  const defaultRange = useMemo(() => getRangeByDays(1), []);

  const [quickRangeKey, setQuickRangeKey] = useState('24h');
  const [startTimestamp, setStartTimestamp] = useState(defaultRange.start);
  const [endTimestamp, setEndTimestamp] = useState(defaultRange.end);
  const [bucketSeconds, setBucketSeconds] = useState(3600);

  const [selectedModel, setSelectedModel] = useState('');
  const [selectedChannel, setSelectedChannel] = useState('');
  const [selectedGroup, setSelectedGroup] = useState('');
  const [userKeyword, setUserKeyword] = useState('');

  const [compareEnabled, setCompareEnabled] = useState(false);
  const [modelTrendTopN, setModelTrendTopN] = useState(8);
  const [modelTrendSortBy, setModelTrendSortBy] = useState('token_used');

  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const [sortBy, setSortBy] = useState('request_count');
  const [sortOrder, setSortOrder] = useState('desc');

  const [loading, setLoading] = useState(false);
  const [errorMessage, setErrorMessage] = useState('');

  const [groupOptions, setGroupOptions] = useState([{ label: '全部分组', value: '' }]);
  const [statData, setStatData] = useState({ quota: 0, rpm: 0, tpm: 0 });
  const [trendItems, setTrendItems] = useState([]);
  const [compareTrendItems, setCompareTrendItems] = useState([]);
  const [modelTrendItems, setModelTrendItems] = useState([]);
  const [modelItems, setModelItems] = useState([]);
  const [channelItems, setChannelItems] = useState([]);
  const [userItems, setUserItems] = useState([]);
  const [userTotal, setUserTotal] = useState(0);

  const requestSeqRef = useRef(0);
  const didInitRef = useRef(false);

  const loadGroups = useCallback(async () => {
    try {
      const response = await API.get('/api/group/');
      const data = Array.isArray(response?.data?.data) ? response.data.data : [];
      const nextOptions = [
        { label: t('全部分组'), value: '' },
        ...data.map((group) => {
          const value = trimToFilterValue(group);
          return {
            label: value || t('默认分组'),
            value,
          };
        }),
      ];
      setGroupOptions(nextOptions);
    } catch {
      setGroupOptions([{ label: t('全部分组'), value: '' }]);
    }
  }, [t]);

  const queryReport = useCallback(
    async ({
      queryStartTimestamp,
      queryEndTimestamp,
      queryBucketSeconds,
      queryModel,
      queryChannel,
      queryGroup,
      queryUserKeyword,
      queryCompareEnabled,
      queryModelTrendTopN,
      queryModelTrendSortBy,
      queryPage,
      queryPageSize,
      querySortBy,
      querySortOrder,
    }) => {
      const normalizedStart = toNumber(queryStartTimestamp);
      const normalizedEnd = toNumber(queryEndTimestamp);
      const normalizedBucket = toNumber(queryBucketSeconds) > 0 ? toNumber(queryBucketSeconds) : 3600;
      const normalizedPage = Math.max(1, toNumber(queryPage));
      const normalizedPageSize = Math.max(1, toNumber(queryPageSize));
      const normalizedChannel = toNumber(queryChannel);
      const normalizedTopN = Math.min(20, Math.max(1, toNumber(queryModelTrendTopN) || 8));
      const trimmedModel = trimToFilterValue(queryModel);
      const trimmedGroup = trimToFilterValue(queryGroup);
      const trimmedUserKeyword = trimToFilterValue(queryUserKeyword);
      const shouldCompare = !!queryCompareEnabled;

      if (normalizedStart <= 0 || normalizedEnd <= 0) {
        showError(t('开始/结束时间戳必须大于 0'));
        return;
      }
      if (normalizedStart > normalizedEnd) {
        showError(t('开始时间不能大于结束时间'));
        return;
      }

      const requestSeq = ++requestSeqRef.current;
      setLoading(true);
      setErrorMessage('');

      try {
        const sharedParams = {
          start_timestamp: normalizedStart,
          end_timestamp: normalizedEnd,
        };
        if (trimmedModel) {
          sharedParams.model_name = trimmedModel;
        }
        if (normalizedChannel > 0) {
          sharedParams.channel = normalizedChannel;
        }
        if (trimmedGroup) {
          sharedParams.group = trimmedGroup;
        }

        const userParams = {
          ...sharedParams,
          p: normalizedPage - 1,
          page_size: normalizedPageSize,
          sort_by: querySortBy,
          sort_order: querySortOrder,
        };
        if (trimmedUserKeyword) {
          userParams.user_keyword = trimmedUserKeyword;
        }

        const [trendRes, modelRes, channelRes, userRes, statRes] = await Promise.all([
          API.get('/api/log/usage_report/trend', {
            params: {
              ...sharedParams,
              bucket_seconds: normalizedBucket,
            },
          }),
          API.get('/api/log/usage_report/model', {
            params: {
              ...sharedParams,
              limit: 200,
            },
          }),
          API.get('/api/log/usage_report/channel', {
            params: {
              ...sharedParams,
              limit: 200,
            },
          }),
          API.get('/api/log/usage_report/user', {
            params: userParams,
          }),
          API.get('/api/log/stat', {
            params: sharedParams,
          }),
        ]);

        if (requestSeq !== requestSeqRef.current) {
          return;
        }

        const trendData = unwrapData(trendRes);
        const modelData = unwrapData(modelRes);
        const channelData = unwrapData(channelRes);
        const userData = unwrapData(userRes);
        const stat = unwrapData(statRes);

        const nextTrendItems = Array.isArray(trendData.items) ? trendData.items : [];
        const nextModelItems = Array.isArray(modelData.items) ? modelData.items : [];
        const nextChannelItems = Array.isArray(channelData.items) ? channelData.items : [];
        const nextUserItems = Array.isArray(userData.items) ? userData.items : [];

        setTrendItems(nextTrendItems);
        setModelItems(nextModelItems);
        setChannelItems(nextChannelItems);
        setUserItems(nextUserItems);

        const totalFromResponse = toNumber(userData.total);
        setUserTotal(totalFromResponse > 0 ? totalFromResponse : nextUserItems.length);

        setStatData({
          quota: toNumber(stat.quota),
          rpm: toNumber(stat.rpm),
          tpm: toNumber(stat.tpm),
        });

        const sortedModelItems = [...nextModelItems].sort((a, b) => {
          return getSortMetricValue(b, queryModelTrendSortBy) - getSortMetricValue(a, queryModelTrendSortBy);
        });

        const topModelNames = trimmedModel
          ? [trimmedModel]
          : sortedModelItems
              .map((item) => trimToFilterValue(item.model_name))
              .filter(Boolean)
              .slice(0, normalizedTopN);

        const comparePromise = shouldCompare
          ? (() => {
              const previousRange = getPreviousRange(normalizedStart, normalizedEnd);
              return API.get('/api/log/usage_report/trend', {
                params: {
                  ...sharedParams,
                  start_timestamp: previousRange.start,
                  end_timestamp: previousRange.end,
                  bucket_seconds: normalizedBucket,
                },
              });
            })()
          : Promise.resolve(null);

        const modelTrendPromise = topModelNames.length
          ? Promise.all(
              topModelNames.map((modelName) =>
                API.get('/api/log/usage_report/trend', {
                  params: {
                    ...sharedParams,
                    model_name: modelName,
                    bucket_seconds: normalizedBucket,
                  },
                }).then((response) => {
                  const data = unwrapData(response);
                  return {
                    modelName,
                    items: Array.isArray(data.items) ? data.items : [],
                  };
                }),
              ),
            )
          : Promise.resolve([]);

        const [compareRes, modelTrendResponses] = await Promise.all([comparePromise, modelTrendPromise]);

        if (requestSeq !== requestSeqRef.current) {
          return;
        }

        const compareData = compareRes ? unwrapData(compareRes) : {};
        const nextCompareTrendItems = Array.isArray(compareData.items) ? compareData.items : [];
        setCompareTrendItems(shouldCompare ? nextCompareTrendItems : []);

        const mergedModelTrendItems = modelTrendResponses.flatMap((entry) => {
          const modelName = normalizeModelName(entry.modelName);
          return entry.items.map((item) => ({
            timestamp: toNumber(item.timestamp),
            model_name: modelName,
            request_count: toNumber(item.request_count),
            success_count: toNumber(item.success_count),
            error_count: toNumber(item.error_count),
            quota: toNumber(item.quota),
            token_used: toNumber(item.token_used),
          }));
        });

        setModelTrendItems(mergedModelTrendItems);
      } catch (error) {
        if (requestSeq !== requestSeqRef.current) {
          return;
        }

        const msg = error?.response?.data?.message || error?.message || t('请求失败');
        setErrorMessage(msg);
        setTrendItems([]);
        setCompareTrendItems([]);
        setModelItems([]);
        setModelTrendItems([]);
        setChannelItems([]);
        setUserItems([]);
        setUserTotal(0);
        setStatData({ quota: 0, rpm: 0, tpm: 0 });
        showError(msg);
      } finally {
        if (requestSeq === requestSeqRef.current) {
          setLoading(false);
        }
      }
    },
    [t],
  );

  const runQuery = useCallback(
    ({
      nextStartTimestamp = startTimestamp,
      nextEndTimestamp = endTimestamp,
      nextBucketSeconds = bucketSeconds,
      nextSelectedModel = selectedModel,
      nextSelectedChannel = selectedChannel,
      nextSelectedGroup = selectedGroup,
      nextUserKeyword = userKeyword,
      nextCompareEnabled = compareEnabled,
      nextModelTrendTopN = modelTrendTopN,
      nextModelTrendSortBy = modelTrendSortBy,
      nextPage = page,
      nextPageSize = pageSize,
      nextSortBy = sortBy,
      nextSortOrder = sortOrder,
    } = {}) => {
      queryReport({
        queryStartTimestamp: nextStartTimestamp,
        queryEndTimestamp: nextEndTimestamp,
        queryBucketSeconds: nextBucketSeconds,
        queryModel: nextSelectedModel,
        queryChannel: nextSelectedChannel,
        queryGroup: nextSelectedGroup,
        queryUserKeyword: nextUserKeyword,
        queryCompareEnabled: nextCompareEnabled,
        queryModelTrendTopN: nextModelTrendTopN,
        queryModelTrendSortBy: nextModelTrendSortBy,
        queryPage: nextPage,
        queryPageSize: nextPageSize,
        querySortBy: nextSortBy,
        querySortOrder: nextSortOrder,
      });
    },
    [
      startTimestamp,
      endTimestamp,
      bucketSeconds,
      selectedModel,
      selectedChannel,
      selectedGroup,
      userKeyword,
      compareEnabled,
      modelTrendTopN,
      modelTrendSortBy,
      page,
      pageSize,
      sortBy,
      sortOrder,
      queryReport,
    ],
  );

  useEffect(() => {
    loadGroups();
  }, [loadGroups]);

  useEffect(() => {
    if (didInitRef.current) {
      return;
    }
    didInitRef.current = true;
    runQuery();
  }, [runQuery]);

  const handleQuickRange = (key, days) => {
    const range = getRangeByDays(days);
    const recommendedBucket = getRecommendedBucketSeconds(days);
    setQuickRangeKey(key);
    setStartTimestamp(range.start);
    setEndTimestamp(range.end);
    setBucketSeconds(recommendedBucket);
    setPage(1);
    runQuery({
      nextStartTimestamp: range.start,
      nextEndTimestamp: range.end,
      nextBucketSeconds: recommendedBucket,
      nextPage: 1,
    });
  };

  const handleRefresh = () => {
    runQuery();
  };

  const handleFilterSearch = () => {
    setPage(1);
    runQuery({ nextPage: 1 });
  };

  const handleReset = () => {
    const range = getRangeByDays(1);
    setQuickRangeKey('24h');
    setStartTimestamp(range.start);
    setEndTimestamp(range.end);
    setBucketSeconds(3600);
    setSelectedModel('');
    setSelectedChannel('');
    setSelectedGroup('');
    setUserKeyword('');
    setCompareEnabled(false);
    setModelTrendTopN(8);
    setModelTrendSortBy('token_used');
    setPage(1);
    setPageSize(DEFAULT_PAGE_SIZE);
    setSortBy('request_count');
    setSortOrder('desc');

    runQuery({
      nextStartTimestamp: range.start,
      nextEndTimestamp: range.end,
      nextBucketSeconds: 3600,
      nextSelectedModel: '',
      nextSelectedChannel: '',
      nextSelectedGroup: '',
      nextUserKeyword: '',
      nextCompareEnabled: false,
      nextModelTrendTopN: 8,
      nextModelTrendSortBy: 'token_used',
      nextPage: 1,
      nextPageSize: DEFAULT_PAGE_SIZE,
      nextSortBy: 'request_count',
      nextSortOrder: 'desc',
    });
  };

  const handleModelDrillDown = useCallback((modelName) => {
    const normalizedModel = trimToFilterValue(modelName);
    if (!normalizedModel) {
      return;
    }
    setSelectedModel(normalizedModel);
    setPage(1);
    runQuery({
      nextSelectedModel: normalizedModel,
      nextPage: 1,
    });
  }, [runQuery]);

  const handlePageChange = (nextPage) => {
    setPage(nextPage);
    runQuery({ nextPage });
  };

  const handlePageSizeChange = (nextPageSize) => {
    setPageSize(nextPageSize);
    setPage(1);
    runQuery({ nextPage: 1, nextPageSize });
  };

  const handleSortFieldChange = (nextSortBy) => {
    setSortBy(nextSortBy);
    setPage(1);
    runQuery({ nextSortBy, nextPage: 1 });
  };

  const handleSortOrderChange = (nextSortOrder) => {
    setSortOrder(nextSortOrder);
    setPage(1);
    runQuery({ nextSortOrder, nextPage: 1 });
  };

  const trendTimeline = useMemo(
    () =>
      trendItems
        .map((item) => {
          const requestCount = toNumber(item.request_count);
          const errorCount = toNumber(item.error_count);
          return {
            timestamp: toNumber(item.timestamp),
            time: formatBucketLabel(item.timestamp, bucketSeconds),
            request_count: requestCount,
            success_count: toNumber(item.success_count),
            error_count: errorCount,
            quota: toNumber(item.quota),
            token_used: toNumber(item.token_used),
            error_rate: requestCount > 0 ? (errorCount * 100) / requestCount : 0,
          };
        })
        .sort((a, b) => a.timestamp - b.timestamp),
    [trendItems, bucketSeconds],
  );

  const compareTrendTimeline = useMemo(
    () =>
      compareTrendItems
        .map((item) => ({
          timestamp: toNumber(item.timestamp),
          time: formatBucketLabel(item.timestamp, bucketSeconds),
          request_count: toNumber(item.request_count),
          success_count: toNumber(item.success_count),
          error_count: toNumber(item.error_count),
          quota: toNumber(item.quota),
          token_used: toNumber(item.token_used),
        }))
        .sort((a, b) => a.timestamp - b.timestamp),
    [compareTrendItems, bucketSeconds],
  );

  const overview = useMemo(() => {
    const result = {
      request_count: 0,
      success_count: 0,
      error_count: 0,
      quota: 0,
      token_used: 0,
    };

    trendTimeline.forEach((item) => {
      result.request_count += item.request_count;
      result.success_count += item.success_count;
      result.error_count += item.error_count;
      result.quota += item.quota;
      result.token_used += item.token_used;
    });

    const requestBase = result.request_count > 0 ? result.request_count : 1;
    const successRate = result.request_count > 0 ? (result.success_count * 100) / requestBase : 0;
    const errorRate = result.request_count > 0 ? (result.error_count * 100) / requestBase : 0;

    return {
      ...result,
      success_rate: successRate,
      error_rate: errorRate,
      active_model_count: modelItems.length,
      active_user_count: userTotal,
      rpm: statData.rpm,
      tpm: statData.tpm,
    };
  }, [trendTimeline, modelItems.length, userTotal, statData.rpm, statData.tpm]);

  const tokenTrendChartData = useMemo(() => {
    const currentLabel = compareEnabled ? t('当前周期 Token') : t('Token 用量');
    const previousLabel = t('上周期 Token');

    const data = trendTimeline.map((item) => ({
      time: item.time,
      metric: currentLabel,
      value: item.token_used,
    }));

    if (!compareEnabled || compareTrendTimeline.length === 0) {
      return data;
    }

    trendTimeline.forEach((item, index) => {
      data.push({
        time: item.time,
        metric: previousLabel,
        value: toNumber(compareTrendTimeline[index]?.token_used),
      });
    });

    return data;
  }, [trendTimeline, compareTrendTimeline, compareEnabled, t]);

  const quotaTrendChartData = useMemo(() => {
    const currentLabel = compareEnabled ? t('当前周期配额') : t('配额消耗');
    const previousLabel = t('上周期配额');

    const data = trendTimeline.map((item) => ({
      time: item.time,
      metric: currentLabel,
      value: item.quota,
    }));

    if (!compareEnabled || compareTrendTimeline.length === 0) {
      return data;
    }

    trendTimeline.forEach((item, index) => {
      data.push({
        time: item.time,
        metric: previousLabel,
        value: toNumber(compareTrendTimeline[index]?.quota),
      });
    });

    return data;
  }, [trendTimeline, compareTrendTimeline, compareEnabled, t]);

  const requestTrendChartData = useMemo(
    () =>
      trendTimeline.flatMap((item) => [
        {
          time: item.time,
          metric: t('总请求'),
          value: item.request_count,
        },
        {
          time: item.time,
          metric: t('成功请求'),
          value: item.success_count,
        },
        {
          time: item.time,
          metric: t('错误请求'),
          value: item.error_count,
        },
      ]),
    [trendTimeline, t],
  );

  const modelTrendChartData = useMemo(
    () =>
      modelTrendItems
        .map((item) => ({
          time: formatBucketLabel(item.timestamp, bucketSeconds),
          timestamp: item.timestamp,
          model_name: normalizeModelName(item.model_name),
          token_used: toNumber(item.token_used),
        }))
        .sort((a, b) => a.timestamp - b.timestamp),
    [modelTrendItems, bucketSeconds],
  );

  const sortedModelItems = useMemo(
    () =>
      [...modelItems].sort(
        (a, b) => getSortMetricValue(b, modelTrendSortBy) - getSortMetricValue(a, modelTrendSortBy),
      ),
    [modelItems, modelTrendSortBy],
  );

  const modelSummaryChartData = useMemo(
    () =>
      sortedModelItems.slice(0, 12).flatMap((item, index) => {
        const modelName = normalizeModelName(item.model_name || t('未命名模型'));
        return [
          {
            key: `${modelName}-request-${index}`,
            model_name: modelName,
            metric: t('请求次数'),
            value: toNumber(item.request_count),
          },
          {
            key: `${modelName}-token-${index}`,
            model_name: modelName,
            metric: t('Token 用量'),
            value: toNumber(item.token_used),
          },
        ];
      }),
    [sortedModelItems, t],
  );

  const channelChartData = useMemo(
    () =>
      channelItems.slice(0, 12).map((item, index) => ({
        key: `${item.channel_id || 0}-${index}`,
        channel_name: normalizeChannelName(item.channel_id, item.channel_name),
        request_count: toNumber(item.request_count),
        token_used: toNumber(item.token_used),
        quota: toNumber(item.quota),
      })),
    [channelItems],
  );

  const modelOptions = useMemo(() => {
    const options = [{ label: t('全部模型'), value: '' }];
    const seen = new Set(['']);
    modelItems.forEach((item) => {
      const rawName = trimToFilterValue(item.model_name);
      if (!rawName || seen.has(rawName)) {
        return;
      }
      seen.add(rawName);
      options.push({ label: rawName, value: rawName });
    });
    return options;
  }, [modelItems, t]);

  const channelOptions = useMemo(() => {
    const options = [{ label: t('全部渠道'), value: '' }];
    const seen = new Set(['']);
    channelItems.forEach((item) => {
      const value = String(toNumber(item.channel_id));
      if (value === '0' || seen.has(value)) {
        return;
      }
      seen.add(value);
      options.push({
        label: normalizeChannelName(item.channel_id, item.channel_name),
        value,
      });
    });
    return options;
  }, [channelItems, t]);

  const sortFieldOptions = useMemo(
    () => [
      { label: t('请求数'), value: 'request_count' },
      { label: t('Token 用量'), value: 'token_used' },
      { label: t('费用/配额'), value: 'quota' },
      { label: t('成功数'), value: 'success_count' },
      { label: t('错误数'), value: 'error_count' },
      { label: t('用户 ID'), value: 'user_id' },
      { label: t('用户名'), value: 'username' },
    ],
    [t],
  );

  const sortOrderOptions = useMemo(
    () => [
      { label: t('降序'), value: 'desc' },
      { label: t('升序'), value: 'asc' },
    ],
    [t],
  );

  const tokenTrendSpec = useMemo(
    () => ({
      type: 'line',
      autoFit: true,
      data: [{ id: 'token_trend', values: tokenTrendChartData }],
      xField: 'time',
      yField: 'value',
      seriesField: 'metric',
      legends: { visible: true, orient: 'top' },
      tooltip: { visible: true },
      axes: [
        { orient: 'bottom', title: { visible: true, text: t('时间') } },
        {
          orient: 'left',
          title: { visible: true, text: t('Token 用量') },
          nice: true,
        },
      ],
      line: {
        style: {
          lineWidth: 2,
          lineDash: (datum) => (String(datum.metric).includes(t('上周期')) ? [6, 4] : [0, 0]),
        },
      },
      point: { visible: false },
    }),
    [tokenTrendChartData, t],
  );

  const quotaTrendSpec = useMemo(
    () => ({
      type: 'line',
      autoFit: true,
      data: [{ id: 'quota_trend', values: quotaTrendChartData }],
      xField: 'time',
      yField: 'value',
      seriesField: 'metric',
      legends: { visible: true, orient: 'top' },
      tooltip: { visible: true },
      axes: [
        { orient: 'bottom', title: { visible: true, text: t('时间') } },
        {
          orient: 'left',
          title: { visible: true, text: t('配额') },
          nice: true,
        },
      ],
      line: {
        style: {
          lineWidth: 2,
          lineDash: (datum) => (String(datum.metric).includes(t('上周期')) ? [6, 4] : [0, 0]),
        },
      },
      point: { visible: false },
    }),
    [quotaTrendChartData, t],
  );

  const requestTrendSpec = useMemo(
    () => ({
      type: 'line',
      autoFit: true,
      data: [{ id: 'request_trend', values: requestTrendChartData }],
      xField: 'time',
      yField: 'value',
      seriesField: 'metric',
      legends: { visible: true, orient: 'top' },
      tooltip: { visible: true },
      axes: [
        { orient: 'bottom', title: { visible: true, text: t('时间') } },
        {
          orient: 'left',
          title: { visible: true, text: t('请求量') },
          nice: true,
        },
      ],
      line: { style: { lineWidth: 2 } },
      point: { visible: false },
    }),
    [requestTrendChartData, t],
  );

  const modelTrendSpec = useMemo(
    () => ({
      type: 'line',
      autoFit: true,
      data: [{ id: 'model_token_trend', values: modelTrendChartData }],
      xField: 'time',
      yField: 'token_used',
      seriesField: 'model_name',
      legends: {
        visible: true,
        orient: 'top',
      },
      tooltip: { visible: true },
      axes: [
        { orient: 'bottom', title: { visible: true, text: t('时间') } },
        {
          orient: 'left',
          title: { visible: true, text: t('Token 用量') },
          nice: true,
        },
      ],
      line: { style: { lineWidth: 2 } },
      point: { visible: false },
    }),
    [modelTrendChartData, t],
  );

  const modelSummarySpec = useMemo(
    () => ({
      type: 'bar',
      autoFit: true,
      data: [{ id: 'model_summary', values: modelSummaryChartData }],
      xField: 'model_name',
      yField: 'value',
      seriesField: 'metric',
      legends: {
        visible: true,
        orient: 'top',
      },
      axes: [
        {
          orient: 'bottom',
          title: { visible: true, text: t('模型') },
          label: {
            formatter: (value) => getAxisModelLabel(value),
            style: {
              angle: -30,
              textAlign: 'right',
              textBaseline: 'top',
            },
          },
        },
        {
          orient: 'left',
          title: {
            visible: true,
            text: t('请求 / Token'),
          },
          nice: true,
        },
      ],
    }),
    [modelSummaryChartData, t],
  );

  const channelSpec = useMemo(
    () => ({
      type: 'bar',
      autoFit: true,
      data: [{ id: 'channel_dist', values: channelChartData }],
      xField: 'channel_name',
      yField: 'token_used',
      legends: { visible: false },
      tooltip: { visible: true },
      axes: [
        {
          orient: 'bottom',
          title: { visible: true, text: t('渠道') },
          label: {
            style: {
              angle: -25,
              textAlign: 'right',
              textBaseline: 'top',
            },
          },
        },
        {
          orient: 'left',
          title: { visible: true, text: t('Token 用量') },
          nice: true,
        },
      ],
    }),
    [channelChartData, t],
  );

  const modelColumns = useMemo(
    () => [
      {
        title: t('模型'),
        dataIndex: 'model_name',
        render: (value) => (
          <Button
            type='tertiary'
            theme='borderless'
            size='small'
            onClick={() => handleModelDrillDown(value)}
          >
            {normalizeModelName(value)}
          </Button>
        ),
      },
      {
        title: t('请求数'),
        dataIndex: 'request_count',
        align: 'right',
        render: (value) => renderNumber(value),
      },
      {
        title: t('Token 用量'),
        dataIndex: 'token_used',
        align: 'right',
        render: (value) => renderNumber(value),
      },
      {
        title: t('费用/配额'),
        dataIndex: 'quota',
        align: 'right',
        render: (value) => renderQuota(value, 6),
      },
      {
        title: t('成功率'),
        dataIndex: 'success_rate',
        align: 'center',
        render: (value) => <Tag color='green'>{formatRate(value)}</Tag>,
      },
      {
        title: t('错误率'),
        dataIndex: 'error_rate',
        align: 'center',
        render: (value) => <Tag color='red'>{formatRate(value)}</Tag>,
      },
    ],
    [t, handleModelDrillDown],
  );

  const userColumns = useMemo(
    () => [
      { title: t('用户 ID'), dataIndex: 'user_id', width: 100, align: 'center' },
      {
        title: t('用户名'),
        dataIndex: 'username',
        width: 180,
        render: (value) => value || '-',
      },
      {
        title: t('请求数'),
        dataIndex: 'request_count',
        width: 120,
        align: 'right',
        render: (value) => renderNumber(value),
      },
      {
        title: t('Token 用量'),
        dataIndex: 'token_used',
        width: 140,
        align: 'right',
        render: (value) => renderNumber(value),
      },
      {
        title: t('费用/配额'),
        dataIndex: 'quota',
        width: 140,
        align: 'right',
        render: (value) => renderQuota(value, 6),
      },
      {
        title: t('成功率'),
        dataIndex: 'success_rate',
        width: 100,
        align: 'center',
        render: (value) => <Tag color='green'>{formatRate(value)}</Tag>,
      },
      {
        title: t('错误率'),
        dataIndex: 'error_rate',
        width: 100,
        align: 'center',
        render: (value) => <Tag color='red'>{formatRate(value)}</Tag>,
      },
    ],
    [t],
  );

  const channelColumns = useMemo(
    () => [
      {
        title: t('渠道'),
        dataIndex: 'channel_name',
        render: (_, record) => normalizeChannelName(record.channel_id, record.channel_name),
      },
      {
        title: t('请求数'),
        dataIndex: 'request_count',
        align: 'right',
        render: (value) => renderNumber(value),
      },
      {
        title: t('Token 用量'),
        dataIndex: 'token_used',
        align: 'right',
        render: (value) => renderNumber(value),
      },
      {
        title: t('成功率'),
        dataIndex: 'success_rate',
        align: 'center',
        render: (value) => <Tag color='green'>{formatRate(value)}</Tag>,
      },
      {
        title: t('错误率'),
        dataIndex: 'error_rate',
        align: 'center',
        render: (value) => <Tag color='red'>{formatRate(value)}</Tag>,
      },
    ],
    [t],
  );

  const modelTableData = useMemo(
    () =>
      sortedModelItems.slice(0, 20).map((item, index) => ({
        ...item,
        key: `${item.model_name || 'model'}-${index}`,
      })),
    [sortedModelItems],
  );

  const userTableData = useMemo(
    () =>
      userItems.map((item, index) => ({
        ...item,
        key: `${item.user_id || 'user'}-${index}`,
      })),
    [userItems],
  );

  const channelTableData = useMemo(
    () =>
      channelItems.map((item, index) => ({
        ...item,
        key: `${item.channel_id || 'channel'}-${index}`,
      })),
    [channelItems],
  );

  const compareRange = useMemo(() => getPreviousRange(startTimestamp, endTimestamp), [startTimestamp, endTimestamp]);

  return (
    <div className='admin-usage-report'>
      <div className='admin-usage-report__stack'>
        {errorMessage ? (
          <Banner
            type='danger'
            bordered
            closeIcon={null}
            description={`${t('请求失败')}: ${errorMessage}`}
          />
        ) : null}

        <Spin spinning={loading} style={FULL_WIDTH_STYLE}>
          <div className='admin-usage-report__stack'>
            <Card title={t('筛选与控制')} bodyStyle={{ padding: 16 }} className='admin-usage-report__filter-card'>
              <div className='admin-usage-report__stack admin-usage-report__gap-sm'>
                <div className='flex flex-wrap items-center gap-2'>
                  {QUICK_RANGES.map((item) => (
                    <Button
                      key={item.key}
                      type='primary'
                      theme={quickRangeKey === item.key ? 'solid' : 'borderless'}
                      onClick={() => handleQuickRange(item.key, item.days)}
                    >
                      {t(item.label)}
                    </Button>
                  ))}

                  <div className='admin-usage-report__inline-switch'>
                    <Typography.Text>{t('周期对比')}</Typography.Text>
                    <Switch
                      checked={compareEnabled}
                      onChange={(checked) => {
                        const nextEnabled = !!checked;
                        setCompareEnabled(nextEnabled);
                        setPage(1);
                        runQuery({
                          nextCompareEnabled: nextEnabled,
                          nextPage: 1,
                        });
                      }}
                    />
                  </div>

                  <Button theme='solid' type='primary' onClick={handleFilterSearch}>
                    {t('查询')}
                  </Button>
                  <Button theme='light' onClick={handleRefresh}>
                    {t('刷新')}
                  </Button>
                  <Button theme='borderless' onClick={handleReset}>
                    {t('重置')}
                  </Button>
                </div>

                <div className='grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-6'>
                  <div>
                    <Typography.Text type='tertiary'>{t('时间粒度')}</Typography.Text>
                    <Select
                      style={FULL_WIDTH_STYLE}
                      value={bucketSeconds}
                      optionList={BUCKET_OPTIONS.map((item) => ({
                        label: t(item.label),
                        value: item.value,
                      }))}
                      onChange={(value) => {
                        const nextBucket = toNumber(value) || 3600;
                        setBucketSeconds(nextBucket);
                        setPage(1);
                        runQuery({
                          nextBucketSeconds: nextBucket,
                          nextPage: 1,
                        });
                      }}
                    />
                  </div>

                  <div>
                    <Typography.Text type='tertiary'>{t('模型筛选')}</Typography.Text>
                    <Select
                      style={FULL_WIDTH_STYLE}
                      value={selectedModel}
                      optionList={modelOptions}
                      onChange={(value) => {
                        const nextModel = trimToFilterValue(value);
                        setSelectedModel(nextModel);
                        setPage(1);
                        runQuery({
                          nextSelectedModel: nextModel,
                          nextPage: 1,
                        });
                      }}
                    />
                  </div>

                  <div>
                    <Typography.Text type='tertiary'>{t('渠道筛选')}</Typography.Text>
                    <Select
                      style={FULL_WIDTH_STYLE}
                      value={selectedChannel}
                      optionList={channelOptions}
                      onChange={(value) => {
                        const nextChannel = trimToFilterValue(value);
                        setSelectedChannel(nextChannel);
                        setPage(1);
                        runQuery({
                          nextSelectedChannel: nextChannel,
                          nextPage: 1,
                        });
                      }}
                    />
                  </div>

                  <div>
                    <Typography.Text type='tertiary'>{t('分组筛选')}</Typography.Text>
                    <Select
                      style={FULL_WIDTH_STYLE}
                      value={selectedGroup}
                      optionList={groupOptions}
                      onChange={(value) => {
                        const nextGroup = trimToFilterValue(value);
                        setSelectedGroup(nextGroup);
                        setPage(1);
                        runQuery({
                          nextSelectedGroup: nextGroup,
                          nextPage: 1,
                        });
                      }}
                    />
                  </div>

                  <div>
                    <Typography.Text type='tertiary'>{t('模型趋势 TopN')}</Typography.Text>
                    <Select
                      style={FULL_WIDTH_STYLE}
                      value={modelTrendTopN}
                      optionList={MODEL_TOP_N_OPTIONS.map((item) => ({
                        label: t(item.label),
                        value: item.value,
                      }))}
                      onChange={(value) => {
                        const nextTopN = Math.min(20, Math.max(1, toNumber(value) || 8));
                        setModelTrendTopN(nextTopN);
                        setPage(1);
                        runQuery({
                          nextModelTrendTopN: nextTopN,
                          nextPage: 1,
                        });
                      }}
                    />
                  </div>

                  <div>
                    <Typography.Text type='tertiary'>{t('模型排序口径')}</Typography.Text>
                    <Select
                      style={FULL_WIDTH_STYLE}
                      value={modelTrendSortBy}
                      optionList={MODEL_SORT_FIELD_OPTIONS.map((item) => ({
                        label: t(item.label),
                        value: item.value,
                      }))}
                      onChange={(value) => {
                        const nextSortBy = trimToFilterValue(value) || 'token_used';
                        setModelTrendSortBy(nextSortBy);
                        setPage(1);
                        runQuery({
                          nextModelTrendSortBy: nextSortBy,
                          nextPage: 1,
                        });
                      }}
                    />
                  </div>
                </div>

                <div className='grid grid-cols-1 gap-3 md:grid-cols-2'>
                  <Input
                    value={userKeyword}
                    placeholder={t('按用户名搜索')}
                    onChange={setUserKeyword}
                    onEnterPress={handleFilterSearch}
                  />
                  <Typography.Text size='small' type='tertiary' className='self-center'>
                    {compareEnabled
                      ? `${t('当前区间')}: ${formatTimestamp(startTimestamp)} ~ ${formatTimestamp(endTimestamp)} | ${t('对比区间')}: ${formatTimestamp(compareRange.start)} ~ ${formatTimestamp(compareRange.end)}`
                      : `${t('时间范围')}: ${formatTimestamp(startTimestamp)} ~ ${formatTimestamp(endTimestamp)}`}
                  </Typography.Text>
                </div>
              </div>
            </Card>

            <Card title={t('全局概览')} bodyStyle={{ padding: 16 }}>
              <div className='grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-5 2xl:grid-cols-10'>
                <MetricCard title={t('总请求数')} value={renderNumber(overview.request_count)} />
                <MetricCard title={t('成功请求')} value={renderNumber(overview.success_count)} />
                <MetricCard title={t('错误请求')} value={renderNumber(overview.error_count)} />
                <MetricCard title={t('成功率')} value={formatRate(overview.success_rate)} tagColor='green' />
                <MetricCard title={t('错误率')} value={formatRate(overview.error_rate)} tagColor='red' />
                <MetricCard title={t('总 Token 用量')} value={renderNumber(overview.token_used)} />
                <MetricCard title={t('总配额')} value={renderQuota(overview.quota, 6)} />
                <MetricCard title={t('近60秒 RPM')} value={renderNumber(overview.rpm)} />
                <MetricCard title={t('近60秒 TPM')} value={renderNumber(overview.tpm)} />
                <MetricCard
                  title={t('活跃模型/用户')}
                  value={`${renderNumber(overview.active_model_count)} / ${renderNumber(overview.active_user_count)}`}
                />
              </div>
            </Card>

            <div className='grid grid-cols-1 gap-4 2xl:grid-cols-3'>
              <Card title={t('整体 Token 趋势')} bodyStyle={{ padding: 16 }} className='2xl:col-span-2'>
                {tokenTrendChartData.length === 0 ? (
                  <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('暂无趋势数据')} />
                ) : (
                  <ChartBox height={360} spec={tokenTrendSpec} />
                )}
              </Card>

              <Card title={t('配额消耗趋势')} bodyStyle={{ padding: 16 }}>
                {quotaTrendChartData.length === 0 ? (
                  <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('暂无趋势数据')} />
                ) : (
                  <ChartBox height={360} spec={quotaTrendSpec} />
                )}
              </Card>
            </div>

            <div className='grid grid-cols-1 gap-4 xl:grid-cols-2'>
              <Card title={t('请求成功/错误趋势')} bodyStyle={{ padding: 16 }}>
                {requestTrendChartData.length === 0 ? (
                  <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('暂无趋势数据')} />
                ) : (
                  <ChartBox height={360} spec={requestTrendSpec} />
                )}
              </Card>

              <Card title={t('模型用量总览（Top12）')} bodyStyle={{ padding: 16 }}>
                {modelSummaryChartData.length === 0 ? (
                  <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('暂无模型数据')} />
                ) : (
                  <ChartBox height={360} spec={modelSummarySpec} />
                )}
              </Card>
            </div>

            <div className='grid grid-cols-1 gap-4 2xl:grid-cols-3'>
              <Card title={t('各模型 Token 趋势（Top）')} bodyStyle={{ padding: 16 }} className='2xl:col-span-2'>
                {modelTrendChartData.length === 0 ? (
                  <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('暂无模型趋势数据')} />
                ) : (
                  <ChartBox height={420} spec={modelTrendSpec} />
                )}
              </Card>

              <Card title={t('模型表现榜')} bodyStyle={{ padding: 16 }}>
                <Table
                  size='small'
                  pagination={false}
                  dataSource={modelTableData}
                  columns={modelColumns}
                  empty={<Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('暂无模型数据')} />}
                />
              </Card>
            </div>

            <div className='grid grid-cols-1 gap-4 2xl:grid-cols-2'>
              <Card title={t('渠道分析')} bodyStyle={{ padding: 16 }}>
                {channelChartData.length === 0 ? (
                  <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('暂无渠道数据')} />
                ) : (
                  <div className='admin-usage-report__stack admin-usage-report__gap-sm'>
                    <ChartBox height={320} spec={channelSpec} />
                    <Table
                      size='small'
                      pagination={false}
                      dataSource={channelTableData.slice(0, 8)}
                      columns={channelColumns}
                    />
                  </div>
                )}
              </Card>

              <Card title={t('用户明细')} bodyStyle={{ padding: 16 }}>
                <div className='admin-usage-report__stack admin-usage-report__gap-sm'>
                  <div className='grid grid-cols-1 gap-3 md:grid-cols-2'>
                    <div>
                      <Typography.Text type='tertiary'>{t('排序字段')}</Typography.Text>
                      <Select
                        value={sortBy}
                        optionList={sortFieldOptions}
                        style={FULL_WIDTH_STYLE}
                        onChange={handleSortFieldChange}
                      />
                    </div>
                    <div>
                      <Typography.Text type='tertiary'>{t('排序方向')}</Typography.Text>
                      <Select
                        value={sortOrder}
                        optionList={sortOrderOptions}
                        style={FULL_WIDTH_STYLE}
                        onChange={handleSortOrderChange}
                      />
                    </div>
                  </div>

                  <Table
                    size='small'
                    pagination={false}
                    scroll={{ x: 980 }}
                    style={{ fontSize: 12 }}
                    dataSource={userTableData}
                    columns={userColumns}
                    empty={<Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('暂无用户数据')} />}
                  />

                  <div className='flex justify-end'>
                    <Pagination
                      total={userTotal}
                      currentPage={page}
                      pageSize={pageSize}
                      showSizeChanger
                      pageSizeOpts={[10, 20, 50, 100]}
                      onPageChange={handlePageChange}
                      onPageSizeChange={handlePageSizeChange}
                    />
                  </div>
                </div>
              </Card>
            </div>
          </div>
        </Spin>
      </div>
    </div>
  );
};

export default AdminUsageReport;
