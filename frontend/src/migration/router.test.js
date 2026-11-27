import { describe, it, expect, beforeEach } from "vitest";
import { getRoute, interceptLinks, navigate, replaceUrl, subscribe } from "./router.js";

function click(link, init = {}) {
  document.body.append(link);
  const event = new MouseEvent("click", { bubbles: true, cancelable: true, button: 0, ...init });
  link.dispatchEvent(event);
  link.remove();
  return event;
}
const anchor = (href, attrs = {}) => Object.assign(document.createElement("a"), { href, ...attrs });

beforeEach(() => navigate("/migrations", { replace: true }));

describe("router", () => {
  it("parses page and parameters from the URL", () => {
    navigate("/migrations/details?database=db-1&id=m1");
    expect(getRoute()).toMatchObject({ page: "details", database: "db-1", id: "m1", request: "" });
    navigate("/migrations/change-request?database=db-1&request=r9");
    expect(getRoute()).toMatchObject({ page: "change-request", request: "r9" });
  });
  it("falls back to index for the bare directory and unknown names", () => {
    navigate("/migrations/");
    expect(getRoute().page).toBe("index");
    navigate("/migrations/nope");
    expect(getRoute().page).toBe("index");
  });
  it("notifies subscribers on navigate but not on replaceUrl", () => {
    const seen = [];
    const off = subscribe((r) => seen.push(r.page));
    navigate("/migrations/history?database=db-1");
    replaceUrl("/migrations/history?database=db-1&tab=console");
    off();
    expect(seen).toEqual(["history"]);
    expect(location.search).toContain("tab=console");
  });
  it("changes the view key when page, database, id or request change", () => {
    navigate("/migrations/details?database=db-1&id=m1");
    const a = getRoute().key;
    navigate("/migrations/details?database=db-1&id=m2");
    expect(getRoute().key).not.toBe(a);
  });
});

describe("interceptLinks", () => {
  it("turns a plain click on an app link into a client-side navigation", () => {
    document.addEventListener("click", interceptLinks, { once: true });
    const event = click(anchor("/migrations/editor?database=db-1"));
    expect(event.defaultPrevented).toBe(true);
    expect(getRoute().page).toBe("editor");
  });
  it.each([
    ["modified click", "/migrations/editor", { ctrlKey: true }],
    ["middle button", "/migrations/editor", { button: 1 }],
  ])("leaves a %s to the browser", (_name, href, init) => {
    document.addEventListener("click", interceptLinks, { once: true });
    const event = click(anchor(href), init);
    expect(event.defaultPrevented).toBe(false);
    document.removeEventListener("click", interceptLinks);
  });
  it("navigates to the other screens of the app too (login, home, admin)", () => {
    for (const [href, path] of [["/admin/users", "/admin/users"], ["/home", "/home"]]) {
      document.addEventListener("click", interceptLinks, { once: true });
      expect(click(anchor(href)).defaultPrevented).toBe(true);
      expect(location.pathname).toBe(path);
    }
  });
  it("leaves API and asset paths, other origins, new-tab and download links alone", () => {
    navigate("/migrations", { replace: true });
    for (const link of [
      anchor("/api/databases"),
      anchor("/assets/main.js"),
      anchor("https://example.com/migrations/editor"),
      anchor("/migrations/editor", { target: "_blank" }),
      anchor("/migrations/editor", { download: "x" }),
    ]) {
      document.addEventListener("click", interceptLinks, { once: true });
      expect(click(link).defaultPrevented).toBe(false);
      document.removeEventListener("click", interceptLinks);
    }
    expect(getRoute().page).toBe("index");
  });
});
