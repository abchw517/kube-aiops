#!/usr/bin/env python3
"""Export each docx prototype index as a single offline HTML file."""

import argparse
import json
import posixpath
import re
from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parents[1]
DOCS = ROOT / "docx"

# Keep source references beside the exporter so an offline copy can be audited
# against the application where the corresponding rules are implemented.
SOURCES = {
    "portal-ui": [
        ("导航随路由和授权变化", "web/src/domain/portal-navigation.ts"),
        ("登录默认本地账号，LDAP 由复选框选择", "web/src/components/states.ts#L25-L30"),
    ],
    "diagnosis-console": [
        ("来源读取、覆盖状态、分页与返回参数", "web/src/views/diagnosis/unified-overview.ts"),
        ("统一记录排序、归并和趋势计算", "web/src/domain/unified-diagnostics.ts"),
    ],
    "operations-knowledge": [
        ("知识快速预览、版本与查询状态", "web/src/views/wiki/wiki.ts"),
        ("授权检索与排序", "api/internal/wiki/operations_read.go#L185"),
    ],
    "system-management": [
        ("角色选择、接口授权、范围与保存预览", "web/src/views/permissions/platform-admin.ts"),
        ("LDAP 测试与认证配置", "web/src/views/permissions/identity-admin.ts"),
        ("会话策略与过期时间", "web/src/views/permissions/session-policy.ts"),
    ],
    "resource-management": [
        ("数据源类型、页签和维护动作", "web/src/views/resources/datasource-management.ts"),
        ("集群列表、录入与维护", "web/src/views/resources/resource-management.ts"),
        ("环境和业务线目录交互", "web/src/views/resources/catalog-workspace.ts"),
    ],
}
ATTRIBUTE = re.compile(r'''\b(href|src)=(["'])([^"'<>\s]+)\2''')
HASH_ASSIGNMENT = re.compile(r"\blocation\.hash\s*=(?!=)\s*([^;\r\n]+);")
HEAD = re.compile(r"<head\b[^>]*>", re.IGNORECASE)
HTML = re.compile(r"<html\b[^>]*>", re.IGNORECASE)


class Index(HTMLParser):
    def __init__(self, html):
        super().__init__()
        self.title = ""
        self.links = []
        self.capture = ""
        self.feed(html)

    def handle_starttag(self, tag, attrs):
        if tag == "h1":
            self.capture = tag
        if tag == "a":
            self.capture = tag
            self.links.append([dict(attrs)["href"], ""])

    def handle_endtag(self, tag):
        if self.capture == tag:
            self.capture = ""

    def handle_data(self, data):
        if self.capture == "h1":
            self.title += data
        if self.capture == "a":
            self.links[-1][1] += data


def local_target(source, href):
    parsed = urlsplit(href)
    if parsed.scheme or parsed.netloc or not parsed.path.endswith(".html"):
        return None
    path = posixpath.normpath(posixpath.join(posixpath.dirname(source), parsed.path))
    if path.startswith("../") or path.startswith("/") or not (DOCS / path).is_file():
        raise ValueError(f"{source}: unresolved prototype link {href}")
    return path + (f"?{parsed.query}" if parsed.query else "") + (f"#{parsed.fragment}" if parsed.fragment else "")


def source_bundle(entry):
    index = Index((DOCS / entry).read_text(encoding="utf-8"))
    views = [[local_target(entry, href), label] for href, label in index.links]
    pages = {}

    def read_page(target):
        path = urlsplit(target).path
        if path in pages:
            return
        pages[path] = None

        def rewrite(match):
            attribute, quote, url = match.groups()
            child = local_target(path, url)
            if child is None:
                return match[0]
            read_page(child)
            if attribute == "href":
                return f'href={quote}#{quote} data-prototype-target={quote}{child}{quote}'
            return f'srcdoc={quote}__PROTOTYPE_FRAME__{child}__END_FRAME__{quote}'

        html = ATTRIBUTE.sub(rewrite, (DOCS / path).read_text(encoding="utf-8"))
        # File-origin blob documents need History API navigation for fragments.
        html = HASH_ASSIGNMENT.sub(r"window.__kubeAIOpsPrototypeNavigate(\1);", html)
        if not HEAD.search(html):
            html, inserted = HTML.subn(lambda match: match[0] + "<head>", html, count=1)
            if not inserted:
                raise ValueError(f"{path}: missing document root")
        pages[path] = html

    for target, _ in views:
        read_page(target)
    return {"title": index.title, "views": views, "pages": pages, "sources": SOURCES[entry.split("/")[0]]}


BRIDGE = """<script>
window.__kubeAIOpsPrototypeNavigate = value => {
  const hash = value.startsWith("#") ? value : "#" + value;
  if (location.hash !== hash) {
    const oldURL = location.href;
    history.pushState(null, "", hash);
    dispatchEvent(new HashChangeEvent("hashchange", {oldURL, newURL: location.href}));
  }
  document.getElementById(hash.slice(1))?.scrollIntoView();
};
document.addEventListener("click", event => {
  const anchor = event.target.closest("a[href]");
  if (!anchor || event.defaultPrevented) return;
  const target = anchor.dataset.prototypeTarget;
  if (target) {
    event.preventDefault();
    window.top.postMessage({prototypeTarget: target, token: "__PROTOTYPE_TOKEN__"}, "*");
    return;
  }
  // Resolve original hash routes explicitly inside the embedded document.
  const href = anchor.getAttribute("href");
  if (href.startsWith("#")) {
    event.preventDefault();
    window.__kubeAIOpsPrototypeNavigate(href);
  }
});
</script>"""

TEMPLATE = r"""<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>kube-aiops · 交互原型</title>
<style>
  * { box-sizing: border-box; }
  body { margin: 0; min-height: 100dvh; display: flex; flex-direction: column; background: #f2f5fa; color: #18243a; font: 15px/1.5 system-ui, sans-serif; }
  header { background: white; padding: .65rem 1rem; border-bottom: 1px solid #dbe3ef; }
  h1 { margin: 0 0 .4rem; font-size: 1.1rem; }
  label { font-weight: 600; }
  select { margin: 0 .45rem; max-width: min(74vw, 32rem); padding: .35rem; background: white; color: inherit; border: 1px solid #929fba; border-radius: .35rem; font: inherit; }
  :focus-visible { outline: 2px solid #484bc2; outline-offset: 2px; }
  details { margin-top: .45rem; }
  summary { width: fit-content; cursor: pointer; }
  ul { padding-left: 1.2rem; margin: .35rem 0; }
  iframe { display: block; width: 100%; min-height: 33rem; flex: 1 0 33rem; border: 0; }
  noscript, #error { margin: 1rem; }
</style>
</head>
<body>
<header>
  <h1 id="title">kube-aiops · 交互原型</h1>
  <label for="views">场景</label><select id="views"></select>
  <details><summary>查看实现依据</summary>
    <p>原型使用演示数据。标注为兼容或评审的场景用于回看旧交互，当前行为以应用实现为准。</p>
    <ul id="sources"></ul>
  </details>
</header>
<noscript>请下载此 HTML 文件，并使用启用 JavaScript 的浏览器打开。</noscript>
<p id="error" role="alert" hidden></p>
<iframe id="preview" title="交互原型"></iframe>
<script id="data" type="application/json">__PROTOTYPE_DATA__</script>
<script>
(() => {
  const data = JSON.parse(document.getElementById("data").textContent);
  const heading = document.getElementById("title");
  const selector = document.getElementById("views");
  const preview = document.getElementById("preview");
  const error = document.getElementById("error");
  const token = Array.from(crypto.getRandomValues(new Uint32Array(4))).join("-");
  const frames = new Map();
  const bridge = data.bridge.replace("__PROTOTYPE_TOKEN__", token);

  const escapeAttribute = value => value.replaceAll("&", "&amp;").replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;").replaceAll('"', "&quot;").replaceAll("'", "&#39;")
    .replaceAll("\n", "&#10;").replaceAll("\r", "&#13;").replaceAll("\\", "&#92;")
    .replaceAll("`", "&#96;").replaceAll("$", "&#36;");

  function documentHTML(target) {
    const [path, query] = target.split("?");
    if (!Object.hasOwn(data.pages, path)) throw new Error("原型未找到");
    let html = data.pages[path];
    if (new URLSearchParams(query).has("embed")) {
      html = html.replace('new URLSearchParams(location.search).has("embed")', "true");
    }
    html = html.replace(/__PROTOTYPE_FRAME__(.+?)__END_FRAME__/g, (_, child) => escapeAttribute(documentHTML(child)));
    const policy = `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; frame-src 'self' blob:; form-action 'none'; base-uri 'none'">`;
    return html.replace(/<head\b[^>]*>/i, head => head + policy + bridge);
  }

  function resolveFrame(target) {
    const [base, fragment] = target.split(/#(.*)/s);
    if (!frames.has(base)) {
      frames.set(base, URL.createObjectURL(new Blob([documentHTML(base)], {type: "text/html;charset=utf-8"})));
    }
    return frames.get(base) + (fragment ? "#" + fragment : "");
  }

  function show(target) {
    try {
      preview.src = resolveFrame(target);
      const match = data.views.find(([path]) => path === target);
      if (match) selector.value = target;
      preview.title = match ? match[1] : heading.textContent;
      error.hidden = true;
    } catch {
      error.textContent = "此场景无法加载。请下载文件后用浏览器打开，再从场景列表切换。";
      error.hidden = false;
    }
  }

  heading.textContent = document.title = data.title;
  for (const [target, label] of data.views) selector.add(new Option(label, target));
  const sources = document.getElementById("sources");
  for (const [label, path] of data.sources) {
    const item = document.createElement("li");
    const link = document.createElement("a");
    link.href = "https://github.com/abchw517/kube-aiops/blob/" + encodeURIComponent(data.sourceRef) + "/" + path;
    link.target = "_blank";
    link.rel = "noopener noreferrer";
    link.textContent = label;
    item.append(link);
    sources.append(item);
  }
  selector.addEventListener("change", () => show(selector.value));
  window.addEventListener("message", event => {
    if (event.data?.token === token && typeof event.data.prototypeTarget === "string") show(event.data.prototypeTarget);
  });
  show(selector.value);
})();
</script>
</body>
</html>
"""


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path, help="directory for the five standalone HTML files")
    parser.add_argument("--source-ref", default="main", help="GitHub source ref for implementation references")
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    for module in SOURCES:
        bundle = source_bundle(f"{module}/prototype.html")
        bundle["bridge"] = BRIDGE
        bundle["sourceRef"] = args.source_ref
        data = json.dumps(bundle, ensure_ascii=False)
        # Prevent HTML inside the data from closing its enclosing script element.
        data = data.replace("<", "\\u003c")
        html = TEMPLATE.replace("__PROTOTYPE_DATA__", data)
        path = args.output / f"kube-aiops-{module}-prototype.html"
        path.write_text(html, encoding="utf-8")
        print(path)


if __name__ == "__main__":
    main()
