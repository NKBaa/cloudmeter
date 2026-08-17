BEGIN;
CREATE TABLE homepage_settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    content_html text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES users(id)
);
INSERT INTO homepage_settings(singleton, content_html) VALUES (true, '<section class="hero-copy"><p class="eyebrow">CloudMeter · 应用云平台</p><h1>把应用部署，变成一件简单的事</h1><p>按需选择 CPU、内存与存储，几分钟内发布你的应用。平台负责网络、计费、版本与备份。</p><div class="home-actions"><a href="/register">免费开始</a><a href="/login">登录控制台</a></div></section><section class="home-features"><article><b>按量计费</b><span>资源用多少付多少，费用清晰可追踪。</span></article><article><b>安全隔离</b><span>应用仅在容器内网通信，公网统一经由网关。</span></article><article><b>可靠发布</b><span>不可变版本、健康检查、备份与快速回滚。</span></article></section>');
COMMIT;
