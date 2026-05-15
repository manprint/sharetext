/* Pure route classifier shared by sw.js (classic worker, importScripts) and
 * the node:test suite (ESM importing a CJS module). Kept side-effect-free
 * apart from attaching `classifyRequest` to the script's enclosing scope.
 */
(function (scope) {
  function classifyRequest({ url, method }) {
    if (!url) return "passthrough";
    let pathname;
    try {
      pathname = new URL(url, "http://x/").pathname;
    } catch {
      return "passthrough";
    }
    // Admin is bypassed for every method so it never lands in any cache and
    // never gets installable as a PWA shell.
    if (pathname === "/admin" || pathname.startsWith("/admin/")) {
      return "bypass";
    }
    const m = (method || "GET").toUpperCase();
    if (m !== "GET" && m !== "HEAD") {
      return "passthrough";
    }
    if (pathname.startsWith("/static/")) return "static-asset";
    if (pathname === "/sw.js" || pathname === "/manifest.webmanifest") return "passthrough";
    if (pathname === "/healthz") return "passthrough";
    if (pathname.startsWith("/ws/")) return "passthrough";
    if (pathname === "/" || pathname.startsWith("/s/")) return "shell";

    const api = pathname.match(/^\/api\/sessions\/([^\/]+)(?:\/(.*))?$/);
    if (api) {
      const tail = api[2] || "";
      if (tail === "") return "api-snapshot";
      if (tail === "files") return "api-files";
      if (tail === "bundle") return "passthrough";
      if (tail.startsWith("files/")) return "api-file-blob";
      return "passthrough";
    }
    return "passthrough";
  }
  scope.classifyRequest = classifyRequest;
  if (typeof module !== "undefined" && module.exports) {
    module.exports = { classifyRequest };
  }
})(typeof self !== "undefined" ? self : (typeof globalThis !== "undefined" ? globalThis : this));
