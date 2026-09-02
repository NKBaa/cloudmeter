<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import {
  Activity,
  ArrowRight,
  Boxes,
  Check,
  ChevronDown,
  Coins,
  CreditCard,
  DatabaseBackup,
  GitCompareArrows,
  Globe,
  HardDrive,
  Headset,
  HelpCircle,
  KeyRound,
  LayoutGrid,
  LogIn,
  Moon,
  Network,
  Rocket,
  Server,
  ShieldCheck,
  Sparkles,
  Sun,
  Terminal,
  Zap,
} from "@lucide/vue";
import BrandMark from "../components/BrandMark.vue";
import { toggleTheme, theme } from "../theme";
import { sanitizeHTML } from "../sanitize-html";
import { useSiteConfig } from "../site-config";

const { systemName, fullSettings } = useSiteConfig();
const route = useRoute();
const router = useRouter();
const inShell = computed(() => route.path.startsWith("/console"));
const content = ref("");
const isLoggedIn = ref(false);
const safeContent = computed(() => sanitizeHTML(content.value));
const aboutURL = computed(() => {
  const value = fullSettings.value?.aboutContent?.trim() || "";
  return /^https?:\/\//i.test(value) ? value : "";
});
const safeAboutContent = computed(() =>
  aboutURL.value
    ? ""
    : sanitizeHTML(fullSettings.value?.aboutContent?.trim() || ""),
);

// FAQ Accordion State
const openFaq = ref<number | null>(0);
function toggleFaq(index: number) {
  openFaq.value = openFaq.value === index ? null : index;
}

// Pricing Toggle State
const isYearly = ref(false);

const features = [
  {
    path: "/auth",
    icon: ShieldCheck,
    title: "租户隔离与权限体系",
    desc: "基于 RBAC 的多租户权限模型，同一用户应用支持内网互通与安全隔离，保障生产环境数据安全。",
  },
  {
    path: "/deploy",
    icon: Boxes,
    title: "模板化极简交付",
    desc: "镜像版本、启动参数、环境变量、Secret 与持久化数据卷一键配置，秒级完成容器实例拉起与健康检查。",
  },
  {
    path: "/metering",
    icon: Coins,
    title: "资源精准按量计费",
    desc: "CPU、内存、持久存储、流量与运行时间独立计量，毫秒级结算，账单流水随时可查，支持余额预警。",
  },
  {
    path: "/gateway",
    icon: Network,
    title: "统一公网入口网关",
    desc: "原生反向代理，支持 WebSocket、SSE、动态路径路由与 HTTPS 自动证书，应用无需直接暴露端口。",
  },
  {
    path: "/backup",
    icon: DatabaseBackup,
    title: "持久数据快照保护",
    desc: "数据卷独立于容器生命周期，支持定时自动快照、手动创建备份以及任意历史版本的一键回滚。",
  },
  {
    path: "/admin",
    icon: LayoutGrid,
    title: "企业级运营管理控制台",
    desc: "产品镜像库、用户赠额、支付流水、工单支持、SMTP 邮件与 Docker 基础设施统一看板。",
  },
];

const steps = [
  {
    n: "01",
    title: "挑选应用模板",
    desc: "在产品市场中挑选官方维护的高性能应用模板，或输入自定义 Docker 镜像地址。",
    code: "$ cloudmeter app create --template open-webui\n✓ Resolving image manifest\n✓ Volume allocated: /data (10GiB)",
  },
  {
    n: "02",
    title: "注入配置与环境变量",
    desc: "一站式配置端口转发、CPU/内存配额限制、API Key 以及持久数据卷挂载点。",
    code: ".env\nPORT=8080\nDATABASE_URL=postgres://...\nOLLAMA_BASE_URL=http://ollama:11434",
  },
  {
    n: "03",
    title: "秒级上线与按量运行",
    desc: "自动化网关分配独立访问二级域名，实例进入监控健康池，按实际用量毫秒计费。",
    code: "$ cloudmeter deploy --env prod\n→ https://open-webui-demo.apps.cloudmeter.local\n● Live in ~4.2s (Healthy)",
  },
];

const faqs = [
  {
    q: "CloudMeter 是如何计费的？",
    a: "CloudMeter 采用精细化按量计费机制，仅在您的容器实例处于运行状态时按 CPU、内存与持久卷存储占用扣费；停机状态下仅收取微量数据卷保存费用。新用户注册即赠送初始测试额度。",
  },
  {
    q: "应用容器需要暴露公网端口吗？",
    a: "不需要。CloudMeter 内置统一反向代理网关，每个应用通过独立子域名进入，内部应用通过隔离虚拟网络互联，极大提升了安全性。",
  },
  {
    q: "容器重启或重新部署后，数据会丢失吗？",
    a: "不会。所有挂载在持久数据卷（Volume）上的数据均独立保存在宿主机高性能存储池中，并支持创建快照备份，容器重新构建与版本升级不会影响已有数据。",
  },
  {
    q: "支持自建私有 Docker 镜像仓库吗？",
    a: "支持。管理员可在控制台配置私有 Registry 凭证与国内镜像加速源，部署时直接拉取私有镜像，同时支持离线镜像包加载。",
  },
];

function handleContentClick(e: MouseEvent) {
  const target = (e.target as HTMLElement).closest("a");
  if (!target) return;
  const href = target.getAttribute("href");
  if (
    isLoggedIn.value &&
    (href === "/login" || href === "/register" || href === "/console")
  ) {
    e.preventDefault();
    router.push("/console");
  }
}

onMounted(async () => {
  isLoggedIn.value = !!localStorage.getItem("session_token");
  try {
    const response = await fetch("/api/homepage");
    if (response.ok) content.value = (await response.json()).contentHtml || "";
  } catch {
    /* Static shell remains available. */
  }
});
</script>

<template>
  <div class="nextdev-marketing">
    <!-- 1. 顶部粘性导航栏 (Header) -->
    <header class="nextdev-header">
      <div class="header-container">
        <div class="header-left">
          <BrandMark />
          <nav class="header-nav">
            <a href="#features">产品能力</a>
            <a href="#how-it-works">工作原理</a>
            <a href="#pricing">计费方案</a>
            <a href="#faq">常见问答</a>
            <RouterLink to="/docs">开发文档</RouterLink>
          </nav>
        </div>
        <div class="header-actions">
          <button
            class="theme-toggle"
            :title="theme === 'dark' ? '切换浅色模式' : '切换深色模式'"
            @click="toggleTheme"
          >
            <Sun v-if="theme === 'dark'" :size="16" />
            <Moon v-else :size="16" />
          </button>
          <template v-if="isLoggedIn">
            <RouterLink class="primary-btn" to="/console">
              <span>控制台</span>
              <ArrowRight :size="15" />
            </RouterLink>
          </template>
          <template v-else>
            <RouterLink class="ghost-btn" to="/login">
              <LogIn :size="15" />
              <span>登录</span>
            </RouterLink>
            <RouterLink class="primary-btn" to="/register">
              <span>开始使用</span>
              <ArrowRight :size="15" />
            </RouterLink>
          </template>
        </div>
      </div>
    </header>

    <!-- 2. Hero 主视觉区域 (HeroSection) -->
    <section class="nextdev-hero aura">
      <div class="hero-grid-bg bg-grid"></div>
      <div class="hero-container">
        <div class="hero-copy">
          <span class="eyebrow">■ {{ systemName }} · 生产级微应用云平台</span>
          <h1 class="hero-title">
            把应用部署，变成一件
            <span class="hero-highlight">
              极其简单的事
              <span class="highlight-line"></span>
            </span>
          </h1>
          <p class="hero-subtitle">
            内置现代化容器编排、按量精准计量、持久数据卷备份、统一反向网关与企业级管理后台。克隆、配置、一键上线。
          </p>
          <div class="hero-actions">
            <RouterLink
              class="primary-btn hero-btn"
              :to="isLoggedIn ? '/console' : '/register'"
            >
              {{ isLoggedIn ? "进入控制台" : "免费开始使用" }}
              <ArrowRight :size="16" />
            </RouterLink>
            <a href="#features" class="ghost-btn hero-btn">
              探索产品能力
            </a>
          </div>
          <!-- 实时指标横条 -->
          <div class="hero-meta">
            <div class="meta-item">
              <span class="meta-dot"></span>
              <strong>99.99%</strong>
              <small>高可用运行时</small>
            </div>
            <div class="meta-divider"></div>
            <div class="meta-item">
              <strong>毫秒级</strong>
              <small>按量计量结算</small>
            </div>
            <div class="meta-divider"></div>
            <div class="meta-item">
              <strong>Docker 27+</strong>
              <small>标准化容器环境</small>
            </div>
          </div>
        </div>

        <!-- 右侧：工程规范读数卡片 (Live Spec-Sheet Preview) -->
        <div class="hero-preview">
          <div class="preview-window">
            <div class="window-bar">
              <div class="window-dots">
                <span></span><span></span><span></span>
              </div>
              <span class="window-title">cloudmeter-node-01.local — specs</span>
              <span class="window-badge">Live</span>
            </div>
            <div class="window-body">
              <div class="telemetry-header">
                <div>
                  <small>集群资源读数</small>
                  <h4>生产集群状态看板</h4>
                </div>
                <span class="live-pill"><Activity :size="13" /> 运行中</span>
              </div>
              <div class="telemetry-stats">
                <div class="stat-box">
                  <span class="stat-label">活跃应用</span>
                  <strong class="stat-num mono-data">12</strong>
                  <span class="stat-foot text-green">全部健康</span>
                </div>
                <div class="stat-box">
                  <span class="stat-label">CPU 负载</span>
                  <strong class="stat-num mono-data">18.4%</strong>
                  <span class="stat-foot">8 核心分配</span>
                </div>
                <div class="stat-box">
                  <span class="stat-label">内存占用</span>
                  <strong class="stat-num mono-data">3.4 GiB</strong>
                  <span class="stat-foot">16 GiB 总量</span>
                </div>
              </div>

              <!-- 示例运行应用卡片 -->
              <div class="preview-app-card">
                <div class="app-icon-wrap">
                  <Boxes :size="20" />
                </div>
                <div class="app-info">
                  <div class="app-heading">
                    <strong>open-webui-prod</strong>
                    <span class="status-pill status-running">Running</span>
                  </div>
                  <small class="mono-data">https://open-webui-demo.apps.cloud.local/</small>
                </div>
                <span class="app-action-badge">访问网关</span>
              </div>

              <!-- 终端读数模拟 -->
              <div class="preview-terminal mono-data">
                <span class="term-dim">[20:19:42]</span> GATEWAY: Ingress route configured for app:open-webui<br />
                <span class="term-dim">[20:19:43]</span> METERING: CPU usage 0.12 cores, Memory 248MB billed<br />
                <span class="term-dim">[20:19:45]</span> HEALTH: Container health check HTTP 200 OK (3ms)
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>

    <!-- 3. 功能特性矩阵 (FeatureGrid) -->
    <section id="features" class="section-container">
      <div class="section-header">
        <div>
          <span class="eyebrow">FEATURES · 核心能力</span>
          <h2 class="section-title">从发布到计费，一套完整闭环</h2>
        </div>
        <p class="section-desc">
          面向现代化容器应用的全生命周期管理，将部署编排、网关转发、按量扣费与数据保护深度整合。
        </p>
      </div>

      <div class="feature-grid">
        <article
          v-for="item in features"
          :key="item.path"
          class="feature-card group"
        >
          <span class="feature-path mono-data">{{ item.path }}</span>
          <div class="feature-icon">
            <component :is="item.icon" :size="20" />
          </div>
          <h3 class="feature-title">{{ item.title }}</h3>
          <p class="feature-desc">{{ item.desc }}</p>
        </article>
      </div>
    </section>

    <!-- 4. 工作流程说明 (HowItWorks) -->
    <section id="how-it-works" class="section-container section-muted">
      <div class="section-header">
        <div>
          <span class="eyebrow">HOW IT WORKS · 极简三步</span>
          <h2 class="section-title">几分钟内完成你的首次应用上线</h2>
        </div>
        <p class="section-desc">
          告别繁琐的服务器配置与运维脚本，用最直观的方式管理你的云端实例。
        </p>
      </div>

      <div class="steps-grid">
        <div v-for="s in steps" :key="s.n" class="step-card">
          <div class="step-header">
            <span class="step-num mono-data">{{ s.n }}</span>
            <span class="step-line"></span>
          </div>
          <h3 class="step-title">{{ s.title }}</h3>
          <p class="step-desc">{{ s.desc }}</p>
          <pre class="step-code mono-data"><code>{{ s.code }}</code></pre>
        </div>
      </div>

      <div class="completion-card">
        <div class="check-icon">
          <Check :size="20" />
        </div>
        <div>
          <strong>全自动化运维保障</strong>
          <p>新版本健康检查通过后平滑切换流量，更新异常自动秒级回退至上一可用版本。</p>
        </div>
      </div>
    </section>

    <!-- 5. 定价方案 (PricingSection) -->
    <section id="pricing" class="section-container">
      <div class="section-header text-center">
        <span class="eyebrow">PRICING · 灵活计费</span>
        <h2 class="section-title">透明合理的按量计费与套餐</h2>
      </div>

      <div class="pricing-grid">
        <!-- Plan 1 -->
        <div class="pricing-card">
          <span class="plan-badge">免费体验</span>
          <h3 class="plan-name">开发测试版</h3>
          <p class="plan-desc">适合轻量级开发测试与小工具部署。</p>
          <div class="plan-price mono-data">
            <strong>¥0.00</strong>
            <small>/ 注册即赠 ¥10</small>
          </div>
          <ul class="plan-features">
            <li><Check :size="15" /> 共享 CPU 突发算力</li>
            <li><Check :size="15" /> 512MB ~ 1GB 内存配置</li>
            <li><Check :size="15" /> 5GiB 持久化数据卷</li>
            <li><Check :size="15" /> 统一二级域名访问</li>
            <li><Check :size="15" /> 社区问答与文档支持</li>
          </ul>
          <RouterLink class="ghost-btn w-full" to="/register">
            立即体验
          </RouterLink>
        </div>

        <!-- Plan 2 (Featured) -->
        <div class="pricing-card featured">
          <span class="plan-badge popular">最受欢迎</span>
          <h3 class="plan-name">按量标准版</h3>
          <p class="plan-desc">适合稳定运行生产业务与常用大型 AI 应用。</p>
          <div class="plan-price mono-data">
            <strong>按量计费</strong>
            <small>/ 毫秒级精准结算</small>
          </div>
          <ul class="plan-features">
            <li><Check :size="15" /> 独享高性能 CPU 核心</li>
            <li><Check :size="15" /> 最高 16GB 弹性内存</li>
            <li><Check :size="15" /> 高性能 SSD 数据卷 & 定时快照</li>
            <li><Check :size="15" /> 支持自定义独立域名 & SSL</li>
            <li><Check :size="15" /> 专属一对一口单支持</li>
          </ul>
          <RouterLink class="primary-btn w-full" to="/register">
            充值开始部署
          </RouterLink>
        </div>

        <!-- Plan 3 -->
        <div class="pricing-card">
          <span class="plan-badge">私有部署</span>
          <h3 class="plan-name">企业专享版</h3>
          <p class="plan-desc">面向企业私有化部署、高并发集群与专属定制需求。</p>
          <div class="plan-price mono-data">
            <strong>联系定制</strong>
            <small>/ 专属节点部署</small>
          </div>
          <ul class="plan-features">
            <li><Check :size="15" /> 多节点 Docker / K8s 集群管理</li>
            <li><Check :size="15" /> 私有镜像 Registry 深度打通</li>
            <li><Check :size="15" /> 99.99% SLA 运行保障</li>
            <li><Check :size="15" /> 审计日志导出与合规支持</li>
            <li><Check :size="15" /> 7x24 技术专家响应</li>
          </ul>
          <RouterLink class="ghost-btn w-full" to="/console/tickets">
            咨询工单
          </RouterLink>
        </div>
      </div>
    </section>

    <!-- 6. 常见问题 (FAQSection) -->
    <section id="faq" class="section-container section-muted">
      <div class="section-header">
        <div>
          <span class="eyebrow">FAQ · 常见问答</span>
          <h2 class="section-title">解答你的所有疑问</h2>
        </div>
        <p class="section-desc">
          涵盖计费规则、数据保护、网络访问与私有镜像支持。
        </p>
      </div>

      <div class="faq-list">
        <div
          v-for="(item, idx) in faqs"
          :key="idx"
          class="faq-item"
          :class="{ open: openFaq === idx }"
          @click="toggleFaq(idx)"
        >
          <div class="faq-q">
            <strong>{{ item.q }}</strong>
            <ChevronDown
              class="faq-arrow"
              :class="{ rotate: openFaq === idx }"
              :size="18"
            />
          </div>
          <div v-show="openFaq === idx" class="faq-a">
            <p>{{ item.a }}</p>
          </div>
        </div>
      </div>
    </section>

    <!-- 7. 行动号召横幅 (CTASection) -->
    <section class="section-container">
      <div class="cta-banner aura">
        <div class="cta-grid-bg bg-grid"></div>
        <div class="cta-content">
          <span class="eyebrow">GET STARTED TODAY</span>
          <h2 class="cta-title">准备好开启你的极简应用云了吗？</h2>
          <p class="cta-subtitle">
            无需复杂的底层配置，注册即送初始测试额度，快速上线你的首个云端应用。
          </p>
          <div class="cta-actions">
            <RouterLink
              class="primary-btn hero-btn"
              :to="isLoggedIn ? '/console' : '/register'"
            >
              {{ isLoggedIn ? "进入控制台" : "立即免费体验" }}
              <ArrowRight :size="16" />
            </RouterLink>
            <RouterLink class="ghost-btn hero-btn" to="/docs">
              阅读使用文档
            </RouterLink>
          </div>
        </div>
      </div>
    </section>

    <!-- 8. 现代化页脚 (Footer) -->
    <footer class="nextdev-footer">
      <div class="footer-container">
        <div class="footer-brand-col">
          <BrandMark />
          <a
            v-if="aboutURL"
            class="footer-desc footer-about-link"
            :href="aboutURL"
            target="_blank"
            rel="noopener noreferrer"
          >了解 {{ systemName }}</a>
          <div v-else-if="safeAboutContent" class="footer-desc" v-html="safeAboutContent"></div>
          <p v-else class="footer-desc">
            {{ systemName }} 是一套现代化的应用云平台，提供按量计量、自动化网关路由、持久数据卷保护与全方位运营控制台。
          </p>
        </div>
        <div class="footer-links-grid">
          <div class="footer-col">
            <h4>产品矩阵</h4>
            <ul>
              <li><a href="#features">应用市场</a></li>
              <li><a href="#how-it-works">容器网关</a></li>
              <li><a href="#pricing">按量计费</a></li>
              <li><RouterLink to="/console/tickets">工单支持</RouterLink></li>
            </ul>
          </div>
          <div class="footer-col">
            <h4>开发者</h4>
            <ul>
              <li><RouterLink to="/docs">开发文档</RouterLink></li>
              <li><RouterLink to="/console">管理控制台</RouterLink></li>
              <li><RouterLink to="/console/faq">常见问题</RouterLink></li>
              <li><RouterLink to="/console/usage">用量监控</RouterLink></li>
            </ul>
          </div>
        </div>
      </div>
      <div class="footer-bottom">
        <span v-if="fullSettings?.footerText" class="mono-data text-xs">
          {{ fullSettings.footerText }}
        </span>
        <span v-else class="mono-data text-xs">
          © {{ new Date().getFullYear() }} {{ systemName }}. All rights reserved.
        </span>
        <div class="footer-bottom-links">
          <a href="#">服务条款</a>
          <a href="#">隐私协议</a>
          <a href="#">系统状态</a>
        </div>
      </div>
    </footer>
  </div>
</template>

<style scoped>
.nextdev-marketing {
  min-height: 100vh;
  background: var(--canvas);
  color: var(--text);
  overflow-x: hidden;
}

/* Header */
.nextdev-header {
  position: sticky;
  top: 0;
  z-index: 50;
  width: 100%;
  border-bottom: 1px solid var(--border);
  background: color-mix(in srgb, var(--canvas) 85%, transparent);
  backdrop-filter: blur(16px);
}
.header-container {
  max-width: 1320px;
  height: 64px;
  margin: 0 auto;
  padding: 0 24px;
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.header-left {
  display: flex;
  align-items: center;
  gap: 32px;
  height: 100%;
}
.header-nav {
  display: flex;
  align-items: center;
  gap: 24px;
  height: 100%;
}
.header-nav a {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 14px;
  font-weight: 500;
  color: var(--text-muted);
  text-decoration: none;
  line-height: 1.5;
  padding: 0 4px;
  transition: color 0.15s ease;
}
.header-nav a:hover {
  color: var(--text);
}
.header-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}
.primary-btn {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 0 16px;
  height: 38px;
  border-radius: 9px;
  font-size: 13px;
  font-weight: 600;
  color: var(--primary-foreground);
  background: var(--primary);
  border: 1px solid transparent;
  text-decoration: none;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.2), 0 0 0 1px rgba(255, 255, 255, 0.1) inset;
  transition: all 0.15s ease;
  cursor: pointer;
}
.primary-btn:hover {
  opacity: 0.92;
  transform: translateY(-1px);
  box-shadow: 0 4px 12px var(--accent-glow);
}
.ghost-btn {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 0 15px;
  height: 38px;
  border-radius: 9px;
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
  background: var(--surface);
  border: 1px solid var(--border);
  text-decoration: none;
  transition: all 0.15s ease;
  cursor: pointer;
}
.ghost-btn:hover {
  background: var(--hover);
  border-color: var(--line-strong);
}

/* Hero Section */
.nextdev-hero {
  position: relative;
  overflow: hidden;
  padding: 80px 24px 100px;
}
.hero-grid-bg {
  position: absolute;
  inset: 0;
  pointer-events: none;
  opacity: 0.6;
  mask-image: radial-gradient(75% 65% at 50% 0%, black, transparent);
  -webkit-mask-image: radial-gradient(75% 65% at 50% 0%, black, transparent);
}
.hero-container {
  max-width: 1320px;
  margin: 0 auto;
  display: grid;
  grid-template-columns: 1.05fr 0.95fr;
  align-items: center;
  gap: 48px;
  position: relative;
  z-index: 1;
}
.hero-copy {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
}
.hero-title {
  margin: 16px 0 20px;
  font-size: clamp(38px, 4.4vw, 62px);
  font-weight: 800;
  line-height: 1.06;
  letter-spacing: -0.04em;
  color: var(--text);
}
.hero-highlight {
  position: relative;
  display: inline-block;
  color: var(--accent);
}
.highlight-line {
  position: absolute;
  bottom: 0;
  left: 0;
  right: 0;
  height: 6px;
  border-radius: 2px;
  background: var(--accent-soft);
}
.hero-subtitle {
  max-width: 560px;
  font-size: 16px;
  line-height: 1.65;
  color: var(--text-muted);
  margin-bottom: 32px;
}
.hero-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 40px;
}
.hero-btn {
  height: 44px;
  padding: 0 22px;
  font-size: 14px;
  border-radius: 10px;
}
.hero-meta {
  display: flex;
  align-items: center;
  gap: 20px;
  padding-top: 24px;
  border-top: 1px solid var(--line);
}
.meta-item {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.meta-item strong {
  font-size: 15px;
  font-family: var(--font-mono);
  color: var(--text);
}
.meta-item small {
  font-size: 11px;
  color: var(--text-muted);
}
.meta-divider {
  width: 1px;
  height: 24px;
  background: var(--line);
}
.meta-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--green);
  box-shadow: 0 0 0 3px var(--accent-soft);
  margin-bottom: 2px;
}

/* Hero Preview Window */
.hero-preview {
  perspective: 1200px;
}
.preview-window {
  border: 1px solid var(--border);
  border-radius: 16px;
  background: var(--paper);
  box-shadow: var(--shadow-lg);
  overflow: hidden;
  transform: rotateY(-3deg) rotateX(1deg);
  transition: transform 0.3s ease, box-shadow 0.3s ease;
}
.preview-window:hover {
  transform: rotateY(0deg) rotateX(0deg);
}
.window-bar {
  height: 40px;
  padding: 0 16px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: var(--surface);
  border-bottom: 1px solid var(--border);
}
.window-dots {
  display: flex;
  gap: 6px;
}
.window-dots span {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--text-muted);
  opacity: 0.4;
}
.window-title {
  font-size: 11px;
  font-family: var(--font-mono);
  color: var(--text-muted);
}
.window-badge {
  font-size: 10px;
  font-weight: 700;
  font-family: var(--font-mono);
  text-transform: uppercase;
  padding: 2px 7px;
  border-radius: 999px;
  background: var(--accent-soft);
  color: var(--accent);
}
.window-body {
  padding: 24px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.telemetry-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.telemetry-header small {
  font-size: 11px;
  font-family: var(--font-mono);
  text-transform: uppercase;
  color: var(--accent);
}
.telemetry-header h4 {
  font-size: 17px;
  font-weight: 700;
  margin: 2px 0 0;
}
.live-pill {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 11px;
  font-weight: 600;
  color: var(--green);
  padding: 3px 9px;
  border-radius: 999px;
  background: rgba(16, 185, 129, 0.12);
  border: 1px solid rgba(16, 185, 129, 0.25);
}
.telemetry-stats {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 10px;
}
.stat-box {
  padding: 12px;
  border: 1px solid var(--line);
  border-radius: 10px;
  background: var(--surface);
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.stat-label {
  font-size: 11px;
  color: var(--text-muted);
}
.stat-num {
  font-size: 18px;
  font-weight: 700;
  color: var(--text);
}
.stat-foot {
  font-size: 10.5px;
  color: var(--text-muted);
}
.text-green {
  color: var(--green) !important;
}
.preview-app-card {
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface);
  display: flex;
  align-items: center;
  gap: 12px;
}
.app-icon-wrap {
  width: 36px;
  height: 36px;
  display: grid;
  place-items: center;
  border-radius: 8px;
  background: var(--accent-soft);
  color: var(--accent);
}
.app-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.app-heading {
  display: flex;
  align-items: center;
  gap: 8px;
}
.app-heading strong {
  font-size: 13px;
  font-weight: 600;
}
.app-info small {
  font-size: 11px;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.app-action-badge {
  font-size: 11px;
  font-weight: 600;
  color: var(--accent);
  padding: 4px 10px;
  border-radius: 6px;
  background: var(--accent-soft);
  white-space: nowrap;
}
.preview-terminal {
  padding: 12px;
  border-radius: 8px;
  background: var(--code-bg);
  border: 1px solid var(--line);
  font-size: 11px;
  line-height: 1.6;
  color: var(--text-soft);
}
.term-dim {
  color: var(--muted-2);
}

/* Sections Common */
.section-container {
  max-width: 1320px;
  margin: 0 auto;
  padding: 96px 24px;
}
.section-muted {
  max-width: 100%;
  background: var(--surface);
}
.section-muted > * {
  max-width: 1320px;
  margin-left: auto;
  margin-right: auto;
}
.section-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  gap: 32px;
  margin-bottom: 48px;
}
.section-title {
  font-size: clamp(28px, 3.2vw, 42px);
  font-weight: 800;
  letter-spacing: -0.03em;
  margin: 10px 0 0;
}
.section-desc {
  max-width: 500px;
  font-size: 15px;
  line-height: 1.65;
  color: var(--text-muted);
  margin: 0;
}

/* Feature Grid */
.feature-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 16px;
}
.feature-card {
  position: relative;
  padding: 28px;
  border: 1px solid var(--line);
  border-radius: 14px;
  background: var(--paper);
  box-shadow: var(--shadow-xs);
  transition: all 0.2s ease;
}
.feature-card:hover {
  border-color: var(--border-soft-3);
  transform: translateY(-2px);
  box-shadow: var(--shadow-sm);
}
.feature-path {
  position: absolute;
  top: 24px;
  right: 24px;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  color: var(--text-muted);
  opacity: 0.6;
}
.feature-icon {
  width: 42px;
  height: 42px;
  display: grid;
  place-items: center;
  border-radius: 10px;
  background: var(--accent-soft);
  color: var(--accent);
  margin-bottom: 20px;
  transition: all 0.2s ease;
}
.feature-card:hover .feature-icon {
  background: var(--accent);
  color: var(--primary-foreground);
}
.feature-title {
  font-size: 16px;
  font-weight: 700;
  margin: 0 0 8px;
}
.feature-desc {
  font-size: 13px;
  line-height: 1.65;
  color: var(--text-muted);
  margin: 0;
}

/* Steps Grid */
.steps-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 24px;
  margin-bottom: 32px;
}
.step-card {
  display: flex;
  flex-direction: column;
}
.step-header {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
}
.step-num {
  font-size: 14px;
  font-weight: 800;
  color: var(--accent);
}
.step-line {
  flex: 1;
  height: 1px;
  background: var(--line);
}
.step-title {
  font-size: 18px;
  font-weight: 700;
  margin: 0 0 8px;
}
.step-desc {
  font-size: 13px;
  line-height: 1.6;
  color: var(--text-muted);
  margin: 0 0 16px;
}
.step-code {
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--paper);
  font-size: 11.5px;
  line-height: 1.6;
  color: var(--text);
  overflow-x: auto;
  white-space: pre-wrap;
}
.completion-card {
  padding: 20px 24px;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--paper);
  display: flex;
  align-items: center;
  gap: 16px;
}
.check-icon {
  width: 38px;
  height: 38px;
  flex: 0 0 38px;
  display: grid;
  place-items: center;
  border-radius: 50%;
  background: rgba(16, 185, 129, 0.14);
  color: var(--green);
}
.completion-card strong {
  display: block;
  font-size: 14px;
  font-weight: 700;
  margin-bottom: 2px;
}
.completion-card p {
  font-size: 12.5px;
  color: var(--text-muted);
  margin: 0;
}

/* Pricing Grid */
.pricing-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 24px;
}
.pricing-card {
  padding: 32px;
  border: 1px solid var(--border);
  border-radius: 16px;
  background: var(--paper);
  box-shadow: var(--shadow-xs);
  display: flex;
  flex-direction: column;
  position: relative;
  transition: all 0.2s ease;
}
.pricing-card.featured {
  border-color: var(--accent);
  box-shadow: 0 0 0 1px var(--accent), var(--shadow-sm);
}
.plan-badge {
  font-size: 11px;
  font-weight: 700;
  font-family: var(--font-mono);
  text-transform: uppercase;
  color: var(--text-muted);
  margin-bottom: 8px;
}
.plan-badge.popular {
  color: var(--accent);
}
.plan-name {
  font-size: 20px;
  font-weight: 800;
  margin: 0 0 6px;
}
.plan-desc {
  font-size: 13px;
  color: var(--text-muted);
  min-height: 38px;
  margin: 0 0 20px;
}
.plan-price {
  padding: 16px 0;
  border-top: 1px solid var(--line);
  border-bottom: 1px solid var(--line);
  display: flex;
  align-items: baseline;
  gap: 6px;
  margin-bottom: 24px;
}
.plan-price strong {
  font-size: 28px;
  font-weight: 800;
}
.plan-price small {
  font-size: 12px;
  color: var(--text-muted);
}
.plan-features {
  list-style: none;
  padding: 0;
  margin: 0 0 32px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  flex: 1;
}
.plan-features li {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 13px;
  color: var(--text);
}
.plan-features li svg {
  color: var(--accent);
  flex: 0 0 15px;
}
.w-full {
  width: 100% !important;
  justify-content: center;
}

/* FAQ List */
.faq-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.faq-item {
  padding: 20px 24px;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--paper);
  cursor: pointer;
  transition: all 0.15s ease;
}
.faq-item:hover {
  border-color: var(--border-soft-2);
}
.faq-q {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
.faq-q strong {
  font-size: 15px;
  font-weight: 600;
}
.faq-arrow {
  color: var(--text-muted);
  transition: transform 0.2s ease;
}
.faq-arrow.rotate {
  transform: rotate(180deg);
}
.faq-a {
  padding-top: 14px;
  margin-top: 12px;
  border-top: 1px solid var(--line);
}
.faq-a p {
  font-size: 13.5px;
  line-height: 1.65;
  color: var(--text-muted);
  margin: 0;
}

/* CTA Banner */
.cta-banner {
  position: relative;
  border: 1px solid var(--border);
  border-radius: 20px;
  background: var(--paper);
  padding: 64px 32px;
  overflow: hidden;
  text-align: center;
}
.cta-grid-bg {
  position: absolute;
  inset: 0;
  pointer-events: none;
  opacity: 0.5;
}
.cta-content {
  position: relative;
  z-index: 1;
  max-width: 640px;
  margin: 0 auto;
  display: flex;
  flex-direction: column;
  align-items: center;
}
.cta-title {
  font-size: clamp(28px, 3.4vw, 42px);
  font-weight: 800;
  letter-spacing: -0.03em;
  margin: 12px 0 16px;
}
.cta-subtitle {
  font-size: 15px;
  line-height: 1.65;
  color: var(--text-muted);
  margin: 0 0 32px;
}
.cta-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

/* Footer */
.nextdev-footer {
  border-top: 1px solid var(--border);
  background: var(--canvas);
  padding: 64px 24px 32px;
}
.footer-container {
  max-width: 1320px;
  margin: 0 auto;
  display: grid;
  grid-template-columns: 1.2fr 1fr;
  gap: 64px;
  margin-bottom: 48px;
}
.footer-desc {
  max-width: 400px;
  font-size: 13px;
  line-height: 1.65;
  color: var(--text-muted);
  margin: 14px 0 0;
}
.footer-about-link {
  display: inline-block;
  text-decoration: none;
}
.footer-about-link:hover {
  color: var(--text);
}
.footer-links-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 32px;
}
.footer-col h4 {
  font-size: 13px;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  margin: 0 0 16px;
  color: var(--text);
}
.footer-col ul {
  list-style: none;
  padding: 0;
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.footer-col a {
  font-size: 13px;
  color: var(--text-muted);
  text-decoration: none;
  transition: color 0.15s ease;
}
.footer-col a:hover {
  color: var(--text);
}
.footer-bottom {
  max-width: 1320px;
  margin: 0 auto;
  padding-top: 24px;
  border-top: 1px solid var(--line);
  display: flex;
  align-items: center;
  justify-content: space-between;
  color: var(--text-muted);
}
.footer-bottom-links {
  display: flex;
  gap: 20px;
}
.footer-bottom-links a {
  font-size: 12px;
  color: var(--text-muted);
  text-decoration: none;
}
.footer-bottom-links a:hover {
  color: var(--text);
}

@media (max-width: 1024px) {
  .hero-container { grid-template-columns: 1fr; }
  .feature-grid { grid-template-columns: repeat(2, 1fr); }
  .steps-grid { grid-template-columns: 1fr; }
  .pricing-grid { grid-template-columns: 1fr; }
  .footer-container { grid-template-columns: 1fr; }
}

@media (max-width: 640px) {
  .header-nav { display: none; }
  .feature-grid { grid-template-columns: 1fr; }
  .hero-actions, .cta-actions { flex-direction: column; width: 100%; }
  .hero-btn { width: 100%; justify-content: center; }
  .footer-links-grid { grid-template-columns: 1fr; }
  .footer-bottom { flex-direction: column; gap: 12px; }
}
</style>
