import { describe, it, expect } from "vitest";
import { ADMIN_PAGE, HOME_PAGE, landingFor } from "./session.js";

const admin = { username: "root", admin: true };
const user = { username: "sam", admin: false };

describe("landingFor", () => {
  it("sends admins to the admin console and everyone else home by default", () => {
    expect(landingFor(admin, null)).toBe(ADMIN_PAGE);
    expect(landingFor(user, null)).toBe(HOME_PAGE);
  });
  it("honours ?next= when the user may open that page", () => {
    expect(landingFor(admin, "/admin/groups")).toBe("/admin/groups");
    expect(landingFor(user, "/migrations/details?database=d&id=m")).toBe("/migrations/details?database=d&id=m");
  });
  it("does not send non-admins to an admin page", () => {
    expect(landingFor(user, "/admin/users")).toBe(HOME_PAGE);
  });
  it("ignores destinations outside the app", () => {
    for (const next of ["https://evil.example", "//evil.example", "/\\evil", "/api/databases", "/login", "/"]) {
      expect(landingFor(admin, next)).toBe(ADMIN_PAGE);
    }
  });
});
