import { execFileSync } from "node:child_process";
import { expect, test, type Page } from "@playwright/test";

const base = () => process.env.DVIZ_URL!;
const p = (s: string) => `${process.env.E2E_PREFIX}${s}`;

async function open(page: Page, host = "local"): Promise<void> {
  await page.goto(`${base()}/#/${host}`);
  await expect(page.locator(`[data-testid=host-tab][data-host=${host}]`)).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByTestId("graph").locator("canvas")).toBeVisible();
}

async function select(page: Page, name: string): Promise<void> {
  await page.getByLabel("Search entities").fill(name);
  await page.getByTestId("search-result").filter({ hasText: name }).first().click();
  await expect(page.getByTestId("details-name")).toHaveText(name);
}

test("one scene per host with display names and deep links", async ({ page }) => {
  await open(page);
  const tabs = page.getByTestId("host-tab");
  await expect(tabs).toHaveText(["Local daemon", "Docker in Docker"]);

  await page.getByLabel("Search entities").fill(process.env.E2E_PREFIX!);
  const results = page.getByTestId("search-result");
  await expect(results.filter({ hasText: p("app") })).toHaveCount(1);
  await expect(results.filter({ hasText: p("dind-only") })).toHaveCount(0);

  await tabs.filter({ hasText: "Docker in Docker" }).click();
  await expect(page).toHaveURL(/#\/dind$/);
  await expect(results.filter({ hasText: p("dind-only") })).toHaveCount(1);
  await expect(results.filter({ hasText: p("app") })).toHaveCount(0);

  // Deep link straight into the second scene.
  await open(page, "dind");
  await page.getByLabel("Search entities").fill(p("dind-only"));
  await expect(results).toHaveCount(1);
});

test("container details mask environment values", async ({ page }) => {
  await open(page);
  await select(page, p("app"));
  await expect(page.getByTestId("container-image")).toHaveText("busybox:1.37");
  await expect(page.getByTestId("container-ports")).toContainText("8080/tcp");
  await expect(page.getByTestId("container-env")).toContainText("SECRET_TOKEN");
  expect(await page.content()).not.toContain(process.env.E2E_SECRET!);
});

test("logs and stats stream live", async ({ page }) => {
  await open(page);
  await select(page, p("app"));
  await page.getByRole("tab", { name: "Logs" }).click();
  await expect(page.getByTestId("logs")).toContainText("hello-e2e");
  await expect(page.getByTestId("logs")).toContainText("tick");

  await page.getByRole("tab", { name: "Stats" }).click();
  await expect(page.getByTestId("stats-mem")).toBeVisible();
  await expect(page.getByTestId("stats-cpu")).toHaveText(/\d+\.\d %/);
});

test("status changes appear without reload", async ({ page }) => {
  await open(page);
  await select(page, p("stopme"));
  await expect(page.getByTestId("details-status")).toHaveText("running");
  execFileSync("docker", ["stop", "-t", "0", p("stopme")]);
  await expect(page.getByTestId("details-status")).toHaveText("exited");
  await expect(page.getByTestId("search-result").filter({ hasText: p("stopme") })).toContainText("exited");
});

test("network tab and reachability mode show who can reach whom", async ({ page }) => {
  await open(page);
  await select(page, p("a"));
  await page.getByRole("tab", { name: "Network" }).click();
  const peers = page.getByTestId("peer");
  await expect(peers.filter({ hasText: p("b") })).toHaveCount(1);
  await expect(peers.filter({ hasText: p("b") }).getByTestId("peer-dns")).toContainText("b-alias");
  await expect(peers.filter({ hasText: p("c") })).toHaveCount(0);

  await page.getByTestId("mode-reachability").click();
  await expect(page.getByTestId("mode-reachability")).toHaveAttribute("aria-checked", "true");
  await expect(page.getByTestId("details-name")).toHaveText(p("a"));
  await expect(page.getByTestId("peer-list")).toContainText(p("b"));
});

test("kind filters apply to search results", async ({ page }) => {
  await open(page);
  await page.getByLabel("Search entities").fill(process.env.E2E_PREFIX!);
  const results = page.getByTestId("search-result");
  await expect(results.filter({ hasText: p("n1") })).toHaveCount(1);
  await expect(results.filter({ hasText: p("a") }).first()).toBeVisible();

  await page.locator("[data-testid=kind-filter][data-kind=container]").uncheck();
  await expect(results.filter({ hasText: "Container" })).toHaveCount(0);
  await expect(results.filter({ hasText: p("n1") })).toHaveCount(1);
});
