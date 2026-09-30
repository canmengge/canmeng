/**
 * list 查重的「前端侧」契约校验。
 *
 * 用同一份共享用例（`services/testdata/listdup_cases.json`）跑**前端那套规则**
 * （`frontend/src/listDuplicate.ts`），断言 kind + 行号与期望一致。
 * 后端侧的对应断言在 `services/list_duplicate_fixture_test.go`：
 *
 *   node 01-源码/scripts/check-listdup.mjs            # 前端规则
 *   go test -run TestListDuplicateFixture ./services  # 后端规则
 *
 * 两边都对着同一份用例，任一侧改了规则没同步，就会有一边失败。
 *
 * 依赖：Node 22.6+ 的 TS 类型擦除（Node 24 默认开启），直接 import .ts 即可。
 */
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "..");
const fixturePath = path.join(root, "services", "testdata", "listdup_cases.json");
const rulesPath = path.join(root, "frontend", "src", "listDuplicate.ts");

const { parseListEntries, findListDuplicates } = await import(pathToFileURL(rulesPath).href);

const fixture = JSON.parse(await readFile(fixturePath, "utf8"));

function issueKey(kind, lines) {
  return `${kind}:${[...lines].sort((a, b) => a - b).join(",")}`;
}

function summary(issues) {
  return issues.map((issue) => issueKey(issue.kind, issue.entries.map((e) => e.line))).sort();
}

let failed = 0;
for (const item of fixture.cases) {
  const entries = parseListEntries(item.text);
  const issues = findListDuplicates(entries);
  const got = summary(issues);
  const want = item.issues.map((issue) => issueKey(issue.kind, issue.lines)).sort();

  if (entries.length !== item.entries) {
    console.error(`✗ [${item.name}] 条目数 = ${entries.length}, 期望 ${item.entries}`);
    failed += 1;
    continue;
  }
  if (got.length !== want.length || got.some((key, i) => key !== want[i])) {
    console.error(`✗ [${item.name}] 问题条目 = ${JSON.stringify(got)}, 期望 ${JSON.stringify(want)}`);
    failed += 1;
    continue;
  }
  console.log(`✓ [${item.name}]`);
}

if (failed > 0) {
  console.error(`\n前端规则与共享用例不一致：${failed} 个用例失败`);
  process.exit(1);
}
console.log(`\n前端规则与共享用例一致（${fixture.cases.length} 个用例）`);
