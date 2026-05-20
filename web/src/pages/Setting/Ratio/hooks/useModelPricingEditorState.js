import { useEffect, useMemo, useState } from 'react';
import { API, showError, showSuccess } from '../../../../helpers';

export const PAGE_SIZE = 10;
export const PRICE_SUFFIX = '$/1M tokens';
const EMPTY_CANDIDATE_MODEL_NAMES = [];

const EMPTY_MODEL = {
  name: '',
  billingMode: 'per-token',
  fixedPrice: '',
  inputPrice: '',
  completionRatioValue: '',
  completionPrice: '',
  defaultCompletionRatio: '',
  cacheRatioValue: '',
  cachePrice: '',
  createCacheRatioValue: '',
  createCachePrice: '',
  imagePrice: '',
  audioInputPrice: '',
  audioOutputPrice: '',
  rawRatios: {
    modelRatio: '',
    completionRatio: '',
    cacheRatio: '',
    createCacheRatio: '',
    imageRatio: '',
    audioRatio: '',
    audioCompletionRatio: '',
  },
  hasConflict: false,
};

const NUMERIC_INPUT_REGEX = /^(\d+(\.\d*)?|\.\d*)?$/;

export const hasValue = (value) =>
  value !== '' && value !== null && value !== undefined && value !== false;

const toNumericString = (value) => {
  if (!hasValue(value) && value !== 0) {
    return '';
  }
  const num = Number(value);
  return Number.isFinite(num) ? String(num) : '';
};

const toNumberOrNull = (value) => {
  if (!hasValue(value) && value !== 0) {
    return null;
  }
  const num = Number(value);
  return Number.isFinite(num) ? num : null;
};

const formatNumber = (value) => {
  const num = toNumberOrNull(value);
  if (num === null) {
    return '';
  }
  return parseFloat(num.toFixed(12)).toString();
};

const toNormalizedNumber = (value) => {
  const formatted = formatNumber(value);
  return formatted === '' ? null : Number(formatted);
};

const derivePriceFromRatio = (basePrice, ratio) => {
  const baseNumber = toNumberOrNull(basePrice);
  const ratioNumber = toNumberOrNull(ratio);
  if (baseNumber === null || ratioNumber === null) {
    return '';
  }
  return formatNumber(baseNumber * ratioNumber);
};

const shouldRefreshDerivedPrice = ({ currentValue, previousBasePrice, ratio }) => {
  if (!hasValue(ratio)) {
    return false;
  }

  if (!hasValue(currentValue)) {
    return true;
  }

  const previousBaseNumber = toNumberOrNull(previousBasePrice);
  if (previousBaseNumber === null) {
    return false;
  }

  const previousDerivedValue = derivePriceFromRatio(previousBaseNumber, ratio);
  return (
    hasValue(previousDerivedValue) &&
    toNormalizedNumber(currentValue) ===
      toNormalizedNumber(previousDerivedValue)
  );
};

const parseOptionJSON = (rawValue) => {
  if (!rawValue || rawValue.trim() === '') {
    return {};
  }
  try {
    const parsed = JSON.parse(rawValue);
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch (error) {
    console.error('JSON解析错误:', error);
    return {};
  }
};

const ratioToBasePrice = (ratio) => {
  const num = toNumberOrNull(ratio);
  if (num === null) return '';
  return formatNumber(num * 2);
};

const normalizeCompletionRatioMeta = (rawMeta) => {
  if (!rawMeta || typeof rawMeta !== 'object' || Array.isArray(rawMeta)) {
    return {
      ratio: '',
    };
  }

  return {
    ratio: toNumericString(rawMeta.ratio),
  };
};

const getPersistedCompletionRatio = (model, ratioValue = model.completionRatioValue) => {
  const nextCompletionRatio = toNormalizedNumber(ratioValue);
  if (nextCompletionRatio === null) {
    return null;
  }

  const defaultCompletionRatio = hasValue(model.defaultCompletionRatio)
    ? toNormalizedNumber(model.defaultCompletionRatio)
    : null;

  const shouldPersistCompletionRatio =
    hasValue(model.rawRatios.completionRatio) ||
    defaultCompletionRatio === null ||
    nextCompletionRatio !== defaultCompletionRatio;

  return shouldPersistCompletionRatio ? nextCompletionRatio : null;
};

const buildModelState = (name, sourceMaps) => {
  const modelRatio = toNumericString(sourceMaps.ModelRatio[name]);
  const completionRatio = toNumericString(sourceMaps.CompletionRatio[name]);
  const completionRatioMeta = normalizeCompletionRatioMeta(
    sourceMaps.CompletionRatioMeta?.[name],
  );
  const effectiveCompletionRatio = hasValue(completionRatio)
    ? completionRatio
    : completionRatioMeta.ratio;
  const cacheRatio = toNumericString(sourceMaps.CacheRatio[name]);
  const createCacheRatio = toNumericString(sourceMaps.CreateCacheRatio[name]);
  const imageRatio = toNumericString(sourceMaps.ImageRatio[name]);
  const audioRatio = toNumericString(sourceMaps.AudioRatio[name]);
  const audioCompletionRatio = toNumericString(
    sourceMaps.AudioCompletionRatio[name],
  );
  const fixedPrice = toNumericString(sourceMaps.ModelPrice[name]);
  const inputPrice = ratioToBasePrice(modelRatio);
  const inputPriceNumber = toNumberOrNull(inputPrice);
  const audioInputPrice =
    inputPriceNumber !== null && hasValue(audioRatio)
      ? formatNumber(inputPriceNumber * Number(audioRatio))
      : '';

  return {
    ...EMPTY_MODEL,
    name,
    billingMode: hasValue(fixedPrice) ? 'per-request' : 'per-token',
    fixedPrice,
    inputPrice,
    completionRatioValue: effectiveCompletionRatio,
    defaultCompletionRatio: completionRatioMeta.ratio,
    completionPrice:
      inputPriceNumber !== null && hasValue(effectiveCompletionRatio)
        ? formatNumber(
            inputPriceNumber * Number(effectiveCompletionRatio),
          )
        : '',
    cacheRatioValue: cacheRatio,
    cachePrice:
      inputPriceNumber !== null && hasValue(cacheRatio)
        ? formatNumber(inputPriceNumber * Number(cacheRatio))
        : '',
    createCacheRatioValue: createCacheRatio,
    createCachePrice:
      inputPriceNumber !== null && hasValue(createCacheRatio)
        ? formatNumber(inputPriceNumber * Number(createCacheRatio))
        : '',
    imagePrice:
      inputPriceNumber !== null && hasValue(imageRatio)
        ? formatNumber(inputPriceNumber * Number(imageRatio))
        : '',
    audioInputPrice,
    audioOutputPrice:
      toNumberOrNull(audioInputPrice) !== null && hasValue(audioCompletionRatio)
        ? formatNumber(Number(audioInputPrice) * Number(audioCompletionRatio))
        : '',
    rawRatios: {
      modelRatio,
      completionRatio,
      cacheRatio,
      createCacheRatio,
      imageRatio,
      audioRatio,
      audioCompletionRatio,
    },
    hasConflict:
      hasValue(fixedPrice) &&
      [
        modelRatio,
        completionRatio,
        cacheRatio,
        createCacheRatio,
        imageRatio,
        audioRatio,
        audioCompletionRatio,
      ].some(hasValue),
  };
};

export const isBasePricingUnset = (model) =>
  !hasValue(model.fixedPrice) && !hasValue(model.inputPrice);

export const getModelWarnings = (model, t) => {
  if (!model) {
    return [];
  }
  const warnings = [];
  const hasDerivedPricing = [
    model.inputPrice,
    model.completionPrice,
    model.cachePrice,
    model.createCachePrice,
    model.imagePrice,
    model.audioInputPrice,
    model.audioOutputPrice,
  ].some(hasValue);

  if (model.hasConflict) {
    warnings.push(
      t('当前模型同时存在按次价格和倍率配置，保存时会按当前计费方式覆盖。'),
    );
  }

  if (
    !hasValue(model.inputPrice) &&
    [
      model.rawRatios.cacheRatio,
      model.rawRatios.createCacheRatio,
      model.rawRatios.imageRatio,
      model.rawRatios.audioRatio,
      model.rawRatios.audioCompletionRatio,
    ].some(hasValue)
  ) {
    warnings.push(
      t(
        '当前模型存在未显式设置输入倍率的扩展倍率；填写输入价格后会自动换算为价格字段。',
      ),
    );
  }

  if (
    model.billingMode === 'per-token' &&
    hasDerivedPricing &&
    !hasValue(model.inputPrice)
  ) {
    warnings.push(t('按量计费下需要先填写输入价格，才能保存其它价格项。'));
  }

  if (
    model.billingMode === 'per-token' &&
    hasValue(model.audioOutputPrice) &&
    !hasValue(model.audioInputPrice)
  ) {
    warnings.push(t('填写音频补全价格前，需要先填写音频输入价格。'));
  }

  return warnings;
};

export const buildSummaryText = (model, t) => {
  if (model.billingMode === 'per-request' && hasValue(model.fixedPrice)) {
    return `${t('按次')} $${model.fixedPrice} / ${t('次')}`;
  }

  if (hasValue(model.inputPrice)) {
    const extraCount = [
      model.completionPrice,
      model.cachePrice,
      model.createCachePrice,
      model.imagePrice,
      model.audioInputPrice,
      model.audioOutputPrice,
    ].filter(hasValue).length;
    const extraLabel =
      extraCount > 0 ? `，${t('额外价格项')} ${extraCount}` : '';
    return `${t('输入')} $${model.inputPrice}${extraLabel}`;
  }

  return t('未设置价格');
};

export const buildOptionalFieldToggles = (model) => ({
  completionPrice:
    hasValue(model.completionRatioValue) || hasValue(model.completionPrice),
  cachePrice: hasValue(model.cacheRatioValue) || hasValue(model.cachePrice),
  createCachePrice:
    hasValue(model.createCacheRatioValue) || hasValue(model.createCachePrice),
  imagePrice: hasValue(model.imagePrice),
  audioInputPrice: hasValue(model.audioInputPrice),
  audioOutputPrice: hasValue(model.audioOutputPrice),
});

const serializeModel = (model, t) => {
  const result = {
    ModelPrice: null,
    ModelRatio: null,
    CompletionRatio: null,
    CacheRatio: null,
    CreateCacheRatio: null,
    ImageRatio: null,
    AudioRatio: null,
    AudioCompletionRatio: null,
  };

  if (model.billingMode === 'per-request') {
    if (hasValue(model.fixedPrice)) {
      result.ModelPrice = toNormalizedNumber(model.fixedPrice);
    }
    return result;
  }

  const inputPrice = toNumberOrNull(model.inputPrice);
  const completionRatio = getPersistedCompletionRatio(model);
  const cacheRatio = toNormalizedNumber(model.cacheRatioValue);
  const createCacheRatio = toNormalizedNumber(model.createCacheRatioValue);
  const imagePrice = toNumberOrNull(model.imagePrice);
  const audioInputPrice = toNumberOrNull(model.audioInputPrice);
  const audioOutputPrice = toNumberOrNull(model.audioOutputPrice);

  const hasDependentPrice = [imagePrice, audioInputPrice, audioOutputPrice].some(
    (value) => value !== null,
  );

  if (inputPrice === null) {
    if (hasDependentPrice) {
      throw new Error(
        t(
          '模型 {{name}} 缺少输入价格，无法计算缓存/图片/音频价格对应的倍率',
          {
            name: model.name,
          },
        ),
      );
    }

    if (hasValue(model.rawRatios.modelRatio)) {
      result.ModelRatio = toNormalizedNumber(model.rawRatios.modelRatio);
    }
    if (completionRatio !== null) {
      result.CompletionRatio = completionRatio;
    }
    if (cacheRatio !== null) {
      result.CacheRatio = cacheRatio;
    }
    if (createCacheRatio !== null) {
      result.CreateCacheRatio = createCacheRatio;
    }
    if (hasValue(model.rawRatios.imageRatio)) {
      result.ImageRatio = toNormalizedNumber(model.rawRatios.imageRatio);
    }
    if (hasValue(model.rawRatios.audioRatio)) {
      result.AudioRatio = toNormalizedNumber(model.rawRatios.audioRatio);
    }
    if (hasValue(model.rawRatios.audioCompletionRatio)) {
      result.AudioCompletionRatio = toNormalizedNumber(
        model.rawRatios.audioCompletionRatio,
      );
    }
    return result;
  }

  result.ModelRatio = toNormalizedNumber(inputPrice / 2);

  if (completionRatio !== null) {
    result.CompletionRatio = completionRatio;
  }
  if (cacheRatio !== null) {
    result.CacheRatio = cacheRatio;
  }
  if (createCacheRatio !== null) {
    result.CreateCacheRatio = createCacheRatio;
  }
  if (imagePrice !== null) {
    result.ImageRatio = toNormalizedNumber(imagePrice / inputPrice);
  }
  if (audioInputPrice !== null) {
    result.AudioRatio = toNormalizedNumber(audioInputPrice / inputPrice);
  }
  if (audioOutputPrice !== null) {
    if (audioInputPrice === null || audioInputPrice === 0) {
      throw new Error(
        t('模型 {{name}} 缺少音频输入价格，无法计算音频补全倍率', {
          name: model.name,
        }),
      );
    }
    result.AudioCompletionRatio = toNormalizedNumber(
      audioOutputPrice / audioInputPrice,
    );
  }

  return result;
};

export const buildPreviewRows = (model, t) => {
  if (!model) return [];

  if (model.billingMode === 'per-request') {
    return [
      {
        key: 'ModelPrice',
        label: 'ModelPrice',
        value: hasValue(model.fixedPrice) ? model.fixedPrice : t('空'),
      },
    ];
  }

  const inputPrice = toNumberOrNull(model.inputPrice);
  if (inputPrice === null) {
    return [
      {
        key: 'ModelRatio',
        label: 'ModelRatio',
        value: hasValue(model.rawRatios.modelRatio)
          ? model.rawRatios.modelRatio
          : t('空'),
      },
      {
        key: 'CompletionRatio',
        label: 'CompletionRatio',
        value:
          getPersistedCompletionRatio(model) !== null
            ? formatNumber(getPersistedCompletionRatio(model))
            : t('空'),
      },
      {
        key: 'CacheRatio',
        label: 'CacheRatio',
        value: hasValue(model.rawRatios.cacheRatio)
          ? model.rawRatios.cacheRatio
          : t('空'),
      },
      {
        key: 'CreateCacheRatio',
        label: 'CreateCacheRatio',
        value: hasValue(model.rawRatios.createCacheRatio)
          ? model.rawRatios.createCacheRatio
          : t('空'),
      },
      {
        key: 'ImageRatio',
        label: 'ImageRatio',
        value: hasValue(model.rawRatios.imageRatio)
          ? model.rawRatios.imageRatio
          : t('空'),
      },
      {
        key: 'AudioRatio',
        label: 'AudioRatio',
        value: hasValue(model.rawRatios.audioRatio)
          ? model.rawRatios.audioRatio
          : t('空'),
      },
      {
        key: 'AudioCompletionRatio',
        label: 'AudioCompletionRatio',
        value: hasValue(model.rawRatios.audioCompletionRatio)
          ? model.rawRatios.audioCompletionRatio
          : t('空'),
      },
    ];
  }

  const completionRatio = getPersistedCompletionRatio(model);
  const cacheRatio = toNormalizedNumber(model.cacheRatioValue);
  const createCacheRatio = toNormalizedNumber(model.createCacheRatioValue);
  const imagePrice = toNumberOrNull(model.imagePrice);
  const audioInputPrice = toNumberOrNull(model.audioInputPrice);
  const audioOutputPrice = toNumberOrNull(model.audioOutputPrice);

  return [
    {
      key: 'ModelRatio',
      label: 'ModelRatio',
      value: formatNumber(inputPrice / 2),
    },
    {
      key: 'CompletionRatio',
      label: 'CompletionRatio',
      value: completionRatio !== null ? formatNumber(completionRatio) : t('空'),
    },
    {
      key: 'CacheRatio',
      label: 'CacheRatio',
      value: cacheRatio !== null ? formatNumber(cacheRatio) : t('空'),
    },
    {
      key: 'CreateCacheRatio',
      label: 'CreateCacheRatio',
      value:
        createCacheRatio !== null ? formatNumber(createCacheRatio) : t('空'),
    },
    {
      key: 'ImageRatio',
      label: 'ImageRatio',
      value:
        imagePrice !== null ? formatNumber(imagePrice / inputPrice) : t('空'),
    },
    {
      key: 'AudioRatio',
      label: 'AudioRatio',
      value:
        audioInputPrice !== null
          ? formatNumber(audioInputPrice / inputPrice)
          : t('空'),
    },
    {
      key: 'AudioCompletionRatio',
      label: 'AudioCompletionRatio',
      value:
        audioOutputPrice !== null &&
        audioInputPrice !== null &&
        audioInputPrice !== 0
          ? formatNumber(audioOutputPrice / audioInputPrice)
          : t('空'),
    },
  ];
};

export function useModelPricingEditorState({
  options,
  refresh,
  t,
  candidateModelNames = EMPTY_CANDIDATE_MODEL_NAMES,
  filterMode = 'all',
}) {
  const [models, setModels] = useState([]);
  const [initialVisibleModelNames, setInitialVisibleModelNames] = useState([]);
  const [selectedModelName, setSelectedModelName] = useState('');
  const [selectedModelNames, setSelectedModelNames] = useState([]);
  const [searchText, setSearchText] = useState('');
  const [currentPage, setCurrentPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [conflictOnly, setConflictOnly] = useState(false);
  const [optionalFieldToggles, setOptionalFieldToggles] = useState({});

  useEffect(() => {
    const sourceMaps = {
      ModelPrice: parseOptionJSON(options.ModelPrice),
      ModelRatio: parseOptionJSON(options.ModelRatio),
      CompletionRatio: parseOptionJSON(options.CompletionRatio),
      CompletionRatioMeta: parseOptionJSON(options.CompletionRatioMeta),
      CacheRatio: parseOptionJSON(options.CacheRatio),
      CreateCacheRatio: parseOptionJSON(options.CreateCacheRatio),
      ImageRatio: parseOptionJSON(options.ImageRatio),
      AudioRatio: parseOptionJSON(options.AudioRatio),
      AudioCompletionRatio: parseOptionJSON(options.AudioCompletionRatio),
    };

    const names = new Set([
      ...candidateModelNames,
      ...Object.keys(sourceMaps.ModelPrice),
      ...Object.keys(sourceMaps.ModelRatio),
      ...Object.keys(sourceMaps.CompletionRatio),
      ...Object.keys(sourceMaps.CompletionRatioMeta),
      ...Object.keys(sourceMaps.CacheRatio),
      ...Object.keys(sourceMaps.CreateCacheRatio),
      ...Object.keys(sourceMaps.ImageRatio),
      ...Object.keys(sourceMaps.AudioRatio),
      ...Object.keys(sourceMaps.AudioCompletionRatio),
    ]);

    const nextModels = Array.from(names)
      .map((name) => buildModelState(name, sourceMaps))
      .sort((a, b) => a.name.localeCompare(b.name));

    setModels(nextModels);
    setInitialVisibleModelNames(
      filterMode === 'unset'
        ? nextModels
            .filter((model) => isBasePricingUnset(model))
            .map((model) => model.name)
        : nextModels.map((model) => model.name),
    );
    setOptionalFieldToggles(
      nextModels.reduce((acc, model) => {
        acc[model.name] = buildOptionalFieldToggles(model);
        return acc;
      }, {}),
    );
    setSelectedModelName((previous) => {
      if (previous && nextModels.some((model) => model.name === previous)) {
        return previous;
      }
      const nextVisibleModels =
        filterMode === 'unset'
          ? nextModels.filter((model) => isBasePricingUnset(model))
          : nextModels;
      return nextVisibleModels[0]?.name || '';
    });
  }, [candidateModelNames, filterMode, options]);

  const visibleModels = useMemo(() => {
    return filterMode === 'unset'
      ? models.filter((model) => initialVisibleModelNames.includes(model.name))
      : models;
  }, [filterMode, initialVisibleModelNames, models]);

  const filteredModels = useMemo(() => {
    return visibleModels.filter((model) => {
      const keyword = searchText.trim().toLowerCase();
      const keywordMatch = keyword
        ? model.name.toLowerCase().includes(keyword)
        : true;
      const conflictMatch = conflictOnly ? model.hasConflict : true;
      return keywordMatch && conflictMatch;
    });
  }, [conflictOnly, searchText, visibleModels]);

  const pagedData = useMemo(() => {
    const start = (currentPage - 1) * PAGE_SIZE;
    return filteredModels.slice(start, start + PAGE_SIZE);
  }, [currentPage, filteredModels]);

  const selectedModel = useMemo(
    () =>
      visibleModels.find((model) => model.name === selectedModelName) || null,
    [selectedModelName, visibleModels],
  );

  const selectedWarnings = useMemo(
    () => getModelWarnings(selectedModel, t),
    [selectedModel, t],
  );

  const previewRows = useMemo(
    () => buildPreviewRows(selectedModel, t),
    [selectedModel, t],
  );

  useEffect(() => {
    setCurrentPage(1);
  }, [searchText, conflictOnly, filterMode, candidateModelNames]);

  useEffect(() => {
    setSelectedModelNames((previous) =>
      previous.filter((name) =>
        visibleModels.some((model) => model.name === name),
      ),
    );
  }, [visibleModels]);

  useEffect(() => {
    if (visibleModels.length === 0) {
      setSelectedModelName('');
      return;
    }
    if (!visibleModels.some((model) => model.name === selectedModelName)) {
      setSelectedModelName(visibleModels[0].name);
    }
  }, [selectedModelName, visibleModels]);

  const upsertModel = (name, updater) => {
    setModels((previous) =>
      previous.map((model) => {
        if (model.name !== name) return model;
        return typeof updater === 'function' ? updater(model) : updater;
      }),
    );
  };

  const isOptionalFieldEnabled = (model, field) => {
    if (!model) return false;
    const modelToggles = optionalFieldToggles[model.name];
    if (modelToggles && typeof modelToggles[field] === 'boolean') {
      return modelToggles[field];
    }
    return buildOptionalFieldToggles(model)[field];
  };

  const updateOptionalFieldToggle = (modelName, field, checked) => {
    setOptionalFieldToggles((prev) => ({
      ...prev,
      [modelName]: {
        ...(prev[modelName] || {}),
        [field]: checked,
      },
    }));
  };

  const handleOptionalFieldToggle = (field, checked) => {
    if (!selectedModel) return;

    updateOptionalFieldToggle(selectedModel.name, field, checked);

    if (checked) {
      if (field === 'completionPrice') {
        upsertModel(selectedModel.name, (model) => {
          const restoredCompletionRatio = hasValue(model.completionRatioValue)
            ? model.completionRatioValue
            : hasValue(model.rawRatios.completionRatio)
              ? model.rawRatios.completionRatio
              : model.defaultCompletionRatio;

          return {
            ...model,
            completionRatioValue: restoredCompletionRatio,
            completionPrice: derivePriceFromRatio(
              model.inputPrice,
              restoredCompletionRatio,
            ),
          };
        });
      } else if (field === 'cachePrice') {
        upsertModel(selectedModel.name, (model) => {
          const restoredCacheRatio = hasValue(model.cacheRatioValue)
            ? model.cacheRatioValue
            : model.rawRatios.cacheRatio;

          return {
            ...model,
            cacheRatioValue: restoredCacheRatio,
            cachePrice: derivePriceFromRatio(model.inputPrice, restoredCacheRatio),
          };
        });
      } else if (field === 'createCachePrice') {
        upsertModel(selectedModel.name, (model) => {
          const restoredCreateCacheRatio = hasValue(model.createCacheRatioValue)
            ? model.createCacheRatioValue
            : model.rawRatios.createCacheRatio;

          return {
            ...model,
            createCacheRatioValue: restoredCreateCacheRatio,
            createCachePrice: derivePriceFromRatio(
              model.inputPrice,
              restoredCreateCacheRatio,
            ),
          };
        });
      }
      return;
    }

    upsertModel(selectedModel.name, (model) => {
      const nextModel = { ...model, [field]: '' };

      if (field === 'completionPrice') {
        nextModel.completionRatioValue = '';
      }

      if (field === 'cachePrice') {
        nextModel.cacheRatioValue = '';
      }

      if (field === 'createCachePrice') {
        nextModel.createCacheRatioValue = '';
      }

      if (field === 'audioInputPrice') {
        nextModel.audioOutputPrice = '';
        setOptionalFieldToggles((prev) => ({
          ...prev,
          [selectedModel.name]: {
            ...(prev[selectedModel.name] || {}),
            audioInputPrice: false,
            audioOutputPrice: false,
          },
        }));
      }

      return nextModel;
    });
  };

  const fillDerivedPricesFromBase = (model, nextInputPrice) => {
    const audioOutputRatio =
      hasValue(model.rawRatios.audioRatio) &&
      hasValue(model.rawRatios.audioCompletionRatio)
        ? Number(model.rawRatios.audioRatio) *
          Number(model.rawRatios.audioCompletionRatio)
        : '';

    const nextModel = {
      ...model,
      inputPrice: nextInputPrice,
    };

    if (
      shouldRefreshDerivedPrice({
        currentValue: model.completionPrice,
        previousBasePrice: model.inputPrice,
        ratio: model.completionRatioValue,
      })
    ) {
      nextModel.completionPrice = derivePriceFromRatio(
        nextInputPrice,
        model.completionRatioValue,
      );
    }

    if (
      shouldRefreshDerivedPrice({
        currentValue: model.cachePrice,
        previousBasePrice: model.inputPrice,
        ratio: model.cacheRatioValue,
      })
    ) {
      nextModel.cachePrice = derivePriceFromRatio(
        nextInputPrice,
        model.cacheRatioValue,
      );
    }

    if (
      shouldRefreshDerivedPrice({
        currentValue: model.createCachePrice,
        previousBasePrice: model.inputPrice,
        ratio: model.createCacheRatioValue,
      })
    ) {
      nextModel.createCachePrice = derivePriceFromRatio(
        nextInputPrice,
        model.createCacheRatioValue,
      );
    }

    if (
      shouldRefreshDerivedPrice({
        currentValue: model.imagePrice,
        previousBasePrice: model.inputPrice,
        ratio: model.rawRatios.imageRatio,
      })
    ) {
      nextModel.imagePrice = derivePriceFromRatio(
        nextInputPrice,
        model.rawRatios.imageRatio,
      );
    }

    if (
      shouldRefreshDerivedPrice({
        currentValue: model.audioInputPrice,
        previousBasePrice: model.inputPrice,
        ratio: model.rawRatios.audioRatio,
      })
    ) {
      nextModel.audioInputPrice = derivePriceFromRatio(
        nextInputPrice,
        model.rawRatios.audioRatio,
      );
    }

    if (
      shouldRefreshDerivedPrice({
        currentValue: model.audioOutputPrice,
        previousBasePrice: model.inputPrice,
        ratio: audioOutputRatio,
      })
    ) {
      nextModel.audioOutputPrice = derivePriceFromRatio(
        nextInputPrice,
        audioOutputRatio,
      );
    }

    return nextModel;
  };

  const handleNumericFieldChange = (field, value) => {
    if (!selectedModel || !NUMERIC_INPUT_REGEX.test(value)) {
      return;
    }

    upsertModel(selectedModel.name, (model) => {
      if (field === 'inputPrice') {
        return fillDerivedPricesFromBase(model, value);
      }

      if (field === 'completionRatioValue') {
        return {
          ...model,
          completionRatioValue: value,
          completionPrice: derivePriceFromRatio(model.inputPrice, value),
        };
      }

      if (field === 'cacheRatioValue') {
        return {
          ...model,
          cacheRatioValue: value,
          cachePrice: derivePriceFromRatio(model.inputPrice, value),
        };
      }

      if (field === 'createCacheRatioValue') {
        return {
          ...model,
          createCacheRatioValue: value,
          createCachePrice: derivePriceFromRatio(model.inputPrice, value),
        };
      }

      return { ...model, [field]: value };
    });
  };

  const handleBillingModeChange = (value) => {
    if (!selectedModel) return;
    upsertModel(selectedModel.name, (model) => ({
      ...model,
      billingMode: value,
    }));
  };

  const addModel = (modelName) => {
    const trimmedName = modelName.trim();
    if (!trimmedName) {
      showError(t('请输入模型名称'));
      return false;
    }
    if (models.some((model) => model.name === trimmedName)) {
      showError(t('模型名称已存在'));
      return false;
    }

    const nextModel = {
      ...EMPTY_MODEL,
      name: trimmedName,
      rawRatios: { ...EMPTY_MODEL.rawRatios },
    };

    setModels((previous) => [nextModel, ...previous]);
    setOptionalFieldToggles((prev) => ({
      ...prev,
      [trimmedName]: buildOptionalFieldToggles(nextModel),
    }));
    setSelectedModelName(trimmedName);
    setCurrentPage(1);
    return true;
  };

  const deleteModel = (name) => {
    const nextModels = models.filter((model) => model.name !== name);
    setModels(nextModels);
    setOptionalFieldToggles((prev) => {
      const next = { ...prev };
      delete next[name];
      return next;
    });
    setSelectedModelNames((previous) =>
      previous.filter((item) => item !== name),
    );
    if (selectedModelName === name) {
      setSelectedModelName(nextModels[0]?.name || '');
    }
  };

  const applySelectedModelPricing = () => {
    if (!selectedModel) {
      showError(t('请先选择一个作为模板的模型'));
      return false;
    }
    if (selectedModelNames.length === 0) {
      showError(t('请先勾选需要批量设置的模型'));
      return false;
    }

    const sourceToggles = optionalFieldToggles[selectedModel.name] || {};

    setModels((previous) =>
      previous.map((model) => {
        if (!selectedModelNames.includes(model.name)) {
          return model;
        }

        const nextModel = {
          ...model,
          billingMode: selectedModel.billingMode,
          fixedPrice: selectedModel.fixedPrice,
          inputPrice: selectedModel.inputPrice,
          completionRatioValue: selectedModel.completionRatioValue,
          completionPrice: derivePriceFromRatio(
            selectedModel.inputPrice,
            selectedModel.completionRatioValue,
          ),
          cacheRatioValue: selectedModel.cacheRatioValue,
          cachePrice: derivePriceFromRatio(
            selectedModel.inputPrice,
            selectedModel.cacheRatioValue,
          ),
          createCacheRatioValue: selectedModel.createCacheRatioValue,
          createCachePrice: derivePriceFromRatio(
            selectedModel.inputPrice,
            selectedModel.createCacheRatioValue,
          ),
          imagePrice: selectedModel.imagePrice,
          audioInputPrice: selectedModel.audioInputPrice,
          audioOutputPrice: selectedModel.audioOutputPrice,
        };

        return nextModel;
      }),
    );

    setOptionalFieldToggles((previous) => {
      const next = { ...previous };
      selectedModelNames.forEach((modelName) => {
        next[modelName] = {
          completionPrice: Boolean(sourceToggles.completionPrice),
          cachePrice: Boolean(sourceToggles.cachePrice),
          createCachePrice: Boolean(sourceToggles.createCachePrice),
          imagePrice: Boolean(sourceToggles.imagePrice),
          audioInputPrice: Boolean(sourceToggles.audioInputPrice),
          audioOutputPrice:
            Boolean(sourceToggles.audioInputPrice) &&
            Boolean(sourceToggles.audioOutputPrice),
        };
      });
      return next;
    });

    showSuccess(
      t('已将模型 {{name}} 的价格配置批量应用到 {{count}} 个模型', {
        name: selectedModel.name,
        count: selectedModelNames.length,
      }),
    );
    return true;
  };

  const handleSubmit = async () => {
    setLoading(true);
    try {
      const output = {
        ModelPrice: {},
        ModelRatio: {},
        CompletionRatio: {},
        CacheRatio: {},
        CreateCacheRatio: {},
        ImageRatio: {},
        AudioRatio: {},
        AudioCompletionRatio: {},
      };

      for (const model of models) {
        const serialized = serializeModel(model, t);
        Object.entries(serialized).forEach(([key, value]) => {
          if (value !== null) {
            output[key][model.name] = value;
          }
        });
      }

      const requestQueue = Object.entries(output).map(([key, value]) =>
        API.put('/api/option/', {
          key,
          value: JSON.stringify(value, null, 2),
        }),
      );

      const results = await Promise.all(requestQueue);
      for (const res of results) {
        if (!res?.data?.success) {
          throw new Error(res?.data?.message || t('保存失败，请重试'));
        }
      }

      showSuccess(t('保存成功'));
      await refresh();
    } catch (error) {
      console.error('保存失败:', error);
      showError(error.message || t('保存失败，请重试'));
    } finally {
      setLoading(false);
    }
  };

  return {
    models,
    selectedModel,
    selectedModelName,
    selectedModelNames,
    setSelectedModelName,
    setSelectedModelNames,
    searchText,
    setSearchText,
    currentPage,
    setCurrentPage,
    loading,
    conflictOnly,
    setConflictOnly,
    filteredModels,
    pagedData,
    selectedWarnings,
    previewRows,
    isOptionalFieldEnabled,
    handleOptionalFieldToggle,
    handleNumericFieldChange,
    handleBillingModeChange,
    handleSubmit,
    addModel,
    deleteModel,
    applySelectedModelPricing,
  };
}
