import { describe, it, expect } from "vitest";
import { cleanPath, isAppPath, matchPath } from "./paths.js";

describe("matchPath", () => {
  it("knows every screen of the app", () => {
    expect(matchPath("/")).toEqual({ name: "root" });
    expect(matchPath("/login")).toEqual({ name: "login" });
    expect(matchPath("/home")).toEqual({ name: "home" });
    expect(matchPath("/admin/users")).toEqual({ name: "admin", page: "users" });
    expect(matchPath("/admin/policies")).toEqual({ name: "admin", page: "policies" });
    expect(matchPath("/migrations")).toEqual({ name: "migrations", page: "index" });
    expect(matchPath("/migrations/change-request")).toEqual({ name: "migrations", page: "change-request" });
  });
  it("still understands the old .html URLs", () => {
    expect(matchPath("/index.html")).toEqual({ name: "root" });
    expect(matchPath("/login.html")).toEqual({ name: "login" });
    expect(matchPath("/admin/groups.html")).toEqual({ name: "admin", page: "groups" });
    expect(matchPath("/migrations/")).toEqual({ name: "migrations", page: "index" });
    expect(matchPath("/migrations/index.html")).toEqual({ name: "migrations", page: "index" });
    expect(matchPath("/migrations/details.html")).toEqual({ name: "migrations", page: "details" });
  });
  it("rejects what belongs to the server", () => {
    for (const p of ["/api/databases", "/session-manager/me", "/admin-control-panel/users", "/assets/main.js", "/admin/nope", "/admin", "/migrations/nope", "/login/x"]) {
      expect(matchPath(p)).toBe(null);
      expect(isAppPath(p)).toBe(false);
    }
  });
});

describe("cleanPath", () => {
  it("drops .html and trailing slashes and names the workspace home /migrations", () => {
    expect(cleanPath("/login.html")).toBe("/login");
    expect(cleanPath("/index.html")).toBe("/");
    expect(cleanPath("/admin/users.html")).toBe("/admin/users");
    expect(cleanPath("/migrations/")).toBe("/migrations");
    expect(cleanPath("/migrations/index.html")).toBe("/migrations");
    expect(cleanPath("/migrations/details.html")).toBe("/migrations/details");
  });
  it("leaves clean paths alone", () => {
    for (const p of ["/", "/home", "/admin/groups", "/migrations", "/migrations/history"]) expect(cleanPath(p)).toBe(p);
  });
});
