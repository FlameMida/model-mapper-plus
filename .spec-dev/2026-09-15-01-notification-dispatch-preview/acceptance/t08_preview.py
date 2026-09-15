#!/usr/bin/env python3
"""T08 visual: notification preview modal at desktop and 390px."""
from __future__ import annotations

import json
import sys
from pathlib import Path

from playwright.sync_api import sync_playwright

BASE = "http://127.0.0.1:31819"
UI = BASE + "/v0/resource/plugins/model-mapper-plus/index.html"
OUT = Path(__file__).resolve().parent
FACTS: dict = {"console": [], "pageerror": []}


def log_console(msg):
    FACTS["console"].append({"type": msg.type, "text": msg.text})


def log_pageerror(err):
    FACTS["pageerror"].append(str(err))


def login(page):
    page.goto(UI, wait_until="networkidle")
    page.get_by_placeholder("CPA management key").fill("acceptance-key")
    page.get_by_role("button", name="登录").click()
    page.get_by_text("已连接").wait_for(timeout=15000)


def open_notifications(page):
    page.locator(".semi-navigation-item", has_text="通知").click()
    page.get_by_text("全局通知", exact=True).wait_for(timeout=15000)
    page.get_by_text("验收日报").first.wait_for(timeout=10000)


def open_preview(page):
    page.get_by_role("button", name="编辑").first.click()
    page.get_by_role("button", name="预览消息").wait_for(timeout=10000)
    page.get_by_role("button", name="预览消息").click()
    page.get_by_text("结构还原 · 非客户端截图").wait_for(timeout=15000)


def modal_metrics(page):
    dialog = page.locator(".semi-modal").last
    dialog.wait_for(state="visible", timeout=10000)
    box = dialog.bounding_box() or {}
    titles = dialog.locator("div").filter(has_text=" · ").evaluate_all(
        "els => [...new Set(els.map(e => e.textContent.trim()).filter(t => t.includes(' · ') && (t.includes('企业微信') || t.includes('飞书') || t.includes('钉钉'))))]"
    )
    text = dialog.inner_text()
    grid = dialog.locator("div").filter(has_text="企业微信 · markdown").first.locator("xpath=..")
    overflow = dialog.evaluate(
        """el => ({
            clientWidth: el.clientWidth,
            scrollWidth: el.scrollWidth,
            overflowX: getComputedStyle(el).overflowX,
            bodyScrollWidth: el.querySelector('.semi-modal-body') ? el.querySelector('.semi-modal-body').scrollWidth : null,
            bodyClientWidth: el.querySelector('.semi-modal-body') ? el.querySelector('.semi-modal-body').clientWidth : null,
            bodyOverflowX: el.querySelector('.semi-modal-body') ? getComputedStyle(el.querySelector('.semi-modal-body')).overflowX : null,
        })"""
    )
    # Column boxes: the three skin panels.
    cols = dialog.evaluate(
        """el => {
            const nodes = [...el.querySelectorAll('div')].filter(d => {
                const t = (d.firstElementChild && d.firstElementChild.textContent) || '';
                return t.includes('企业微信 · markdown') || t.includes('飞书 · text') || t.includes('钉钉 · markdown');
            });
            return nodes.map(n => {
                const r = n.getBoundingClientRect();
                return {title: n.firstElementChild.textContent.trim(), x: r.x, y: r.y, w: r.width, h: r.height};
            });
        }"""
    )
    lines_ok = ("用量 " in text and "tokens" in text and "占比 " in text)
    # Same line must not mix tokens and 占比 (spec: 指标分行)
    mixed = any(("tokens" in ln and "%" in ln) for ln in text.splitlines())
    return {
        "box": box,
        "titles": titles,
        "overflow": overflow,
        "cols": cols,
        "lines_ok": lines_ok,
        "mixed_metric_line": mixed,
        "has_wecom": "企业微信 · markdown" in text,
        "has_feishu": "飞书 · text" in text,
        "has_dingtalk": "钉钉 · markdown" in text,
        "has_dingtalk_title_note": "会话列表标题：验收日报" in text,
        "sample": "\n".join(text.splitlines()[:20]),
    }


def screenshot_modal(page, name):
    path = OUT / name
    page.locator(".semi-modal").last.screenshot(path=str(path))
    page.screenshot(path=str(OUT / ("page-" + name)), full_page=True)
    return str(path)


def close_preview(page):
    page.get_by_role("button", name="关闭").click()
    page.locator(".semi-modal").wait_for(state="hidden", timeout=10000)
    page.get_by_role("button", name="取消").click()


def put_settings(page, dingtalk_enabled: bool):
    body = {
        "enabled": True,
        "global_default": {},
        "notifications": [{
            "id": "n-t08",
            "name": "验收日报",
            "enabled": True,
            "is_default": True,
            "modules": [{"kind": "daily", "period": "current"}, {"kind": "monthly", "period": "current"}],
            "schedule": {"kind": "interval", "interval": 86400, "time": "09:00:00"},
            "platforms": [
                {"kind": "wecom", "enabled": True, "webhook": "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=t08"},
                {"kind": "feishu", "enabled": True, "webhook": "https://open.feishu.cn/open-apis/bot/v2/hook/t08"},
                {"kind": "dingtalk", "enabled": dingtalk_enabled, "webhook": "https://oapi.dingtalk.com/robot/send?access_token=t08"},
            ],
        }],
    }
    body["global_default"] = body["notifications"][0]
    res = page.evaluate(
        """async (body) => {
            const r = await fetch('/v0/management/plugins/model-mapper-plus/notifications/settings', {
                method: 'PUT', headers: {'Content-Type':'application/json','Authorization':'Bearer acceptance-key'},
                body: JSON.stringify(body)
            });
            return {status: r.status};
        }""",
        body,
    )
    return res


def main() -> int:
    OUT.mkdir(parents=True, exist_ok=True)
    with sync_playwright() as p:
        browser = p.chromium.launch(channel="chrome", headless=True)
        context = browser.new_context(viewport={"width": 1440, "height": 900})
        page = context.new_page()
        page.on("console", log_console)
        page.on("pageerror", log_pageerror)
        login(page)
        try:
            open_notifications(page)
        except Exception:
            page.screenshot(path=str(OUT / "fail-notifications.png"), full_page=True)
            FACTS["fail_html"] = page.content()[:8000]
            raise
        next_fire = page.locator(".semi-table").first.inner_text()
        FACTS["list_text"] = next_fire
        FACTS["next_fire_rfc3339"] = ("T" in next_fire and "+08" in next_fire)

        open_preview(page)
        desktop = modal_metrics(page)
        FACTS["desktop"] = desktop
        screenshot_modal(page, "preview-desktop-3col.png")
        close_preview(page)

        page.set_viewport_size({"width": 390, "height": 844})
        page.reload(wait_until="networkidle")
        login(page)
        open_notifications(page)
        open_preview(page)
        narrow = modal_metrics(page)
        FACTS["narrow"] = narrow
        screenshot_modal(page, "preview-390-3col.png")
        close_preview(page)

        put_settings(page, dingtalk_enabled=False)
        page.set_viewport_size({"width": 1440, "height": 900})
        page.reload(wait_until="networkidle")
        login(page)
        open_notifications(page)
        open_preview(page)
        two = modal_metrics(page)
        FACTS["two_col"] = two
        screenshot_modal(page, "preview-desktop-2col.png")
        close_preview(page)

        browser.close()

    (OUT / "t08-facts.json").write_text(json.dumps(FACTS, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps({k: FACTS[k] for k in ("desktop", "narrow", "two_col", "next_fire_rfc3339")}, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as e:
        FACTS["error"] = repr(e)
        (OUT / "t08-facts.json").write_text(json.dumps(FACTS, ensure_ascii=False, indent=2), encoding="utf-8")
        raise
