export function formatSubscriptionDuration(plan, t) {
  const unit = plan?.duration_unit || 'month';
  const value = plan?.duration_value || 1;
  const unitLabels = {
    year: t('年'),
    month: t('个月'),
    day: t('天'),
    hour: t('小时'),
    custom: t('自定义'),
  };
  if (unit === 'custom') {
    const seconds = plan?.custom_seconds || 0;
    if (seconds >= 86400) return `${Math.floor(seconds / 86400)} ${t('天')}`;
    if (seconds >= 3600) return `${Math.floor(seconds / 3600)} ${t('小时')}`;
    return `${seconds} ${t('秒')}`;
  }
  return `${value} ${unitLabels[unit] || unit}`;
}

export function formatSubscriptionResetPeriod(plan, t) {
  const period = plan?.quota_reset_period || 'never';
  if (period === 'never') return t('不重置');
  if (period === 'daily') return t('每天');
  if (period === 'weekly') return t('每周');
  if (period === 'monthly') return t('每月');
  if (period === 'custom') {
    const seconds = Number(plan?.quota_reset_custom_seconds || 0);
    if (seconds >= 86400) return `${Math.floor(seconds / 86400)} ${t('天')}`;
    if (seconds >= 3600) return `${Math.floor(seconds / 3600)} ${t('小时')}`;
    if (seconds >= 60) return `${Math.floor(seconds / 60)} ${t('分钟')}`;
    return `${seconds} ${t('秒')}`;
  }
  return t('不重置');
}

export function getSubscriptionMeterType(plan) {
  return plan?.meter_type === 'request_count' ? 'request_count' : 'quota';
}

export function formatSubscriptionAmount(plan, amount, t, renderQuota) {
  const meterType = getSubscriptionMeterType(plan);
  const numeric = Number(amount || 0);
  if (numeric < 0) {
    return `-${formatSubscriptionAmount(plan, Math.abs(numeric), t, renderQuota)}`;
  }
  if (numeric === 0) {
    return meterType === 'request_count' ? `0 ${t('次')}` : renderQuota(0);
  }
  if (numeric <= 0) {
    return t('不限');
  }
  if (meterType === 'request_count') {
    return `${numeric} ${t('次')}`;
  }
  return renderQuota(numeric);
}

export function getSubscriptionAmountLabel(plan, t) {
  return getSubscriptionMeterType(plan) === 'request_count'
    ? t('总次数')
    : t('总额度');
}
