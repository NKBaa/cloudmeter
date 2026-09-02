const usageLabels: Record<string, string> = {
  "app.runtime.minutes": "应用运行时长",
  "cpu.core_hours": "CPU 核心用量",
  "memory.gib_hours": "内存用量",
  "storage.system.gib_days": "历史系统盘存储（已停用）",
  "storage.data.gib_days": "共享数据存储",
  "network.egress_gib": "公网出站流量",
  "app.deployment": "应用部署",
  "product.authorization": "产品授权",
  "network.public_ingress": "公网入口",
  "backup.operation": "备份操作",
  "backup.storage.gib_days": "历史备份存储（已合并）",
};

const unitLabels: Record<string, string> = {
  minute: "分钟",
  core_hour: "核时",
  GiB_hour: "GiB·小时",
  GiB_day: "GiB·天",
  GiB: "GiB",
  deployment: "次",
  authorization: "次",
  ingress: "入口·小时",
  operation: "次",
  unit: "单位",
};

export const usageCodeLabel = (code: string) =>
  usageLabels[code] || `自定义费用项（${code}）`;
export const usageUnitLabel = (unit: string) => unitLabels[unit] || unit;
