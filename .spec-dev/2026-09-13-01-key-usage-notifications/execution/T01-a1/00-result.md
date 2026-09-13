# T01-a1 实施回报（ready）

- task_id: T01 / claim_key: T01-a1
- worktree: /Users/maverick/cpa-model-mapper-plus/.worktrees/ku-T01
- branch: ku/T01
- base_commit: f7ace3fe6f37c95ab583cb8d58a47fd5e383d584
- implementation_commit: 077a46cdbee84bf6bc691402c4b114674e361c8a

## 实际升级版本

| 包 | 版本 | 任务下限 |
|---|---|---|
| react / react-dom | 19.3.0 | 19.3.0 |
| @douyinfe/semi-ui / semi-icons | 2.103.0 | 2.103.0 |
| @dnd-kit/core | 6.3.1（新增） | 6.3.1 |
| @dnd-kit/sortable | 10.0.0（新增） | 10.0.0 |
| typescript | 7.0.2 | 7.0.2 |
| vite | 8.3.0 | 8.3.0 |
| @vitejs/plugin-react | 6.1.1 | 6.1.1 |
| vitest | 5.0.0 | 5.0.0 |
| jsdom | 30.0.1 | 30.0.1 |
| @testing-library/react | 16.3.3 | latest |

pnpm view 复核：全部目标包最新稳定版恰为任务下限，无更高版可取。

## 三条验证命令

| 命令 | 退出码 | 证据 |
|---|---|---|
| pnpm --dir web run typecheck（tsc --noEmit，TS 7.0.2） | 0 | 05-typecheck.txt |
| pnpm --dir web test（vitest run，vitest 5.0.0） | 0 | 06-vitest-first-run.txt |
| make web-build（pnpm install --frozen-lockfile + VITE_HOSTED=1 pnpm run build） | 0 | 07-make-web-build.txt |

vitest 5：20 文件 / 140 测试全绿；`server.deps.inline` 配置仍被接受，vite.config.ts / vitest.config.ts / src/test/setup.ts 均无需改动。

## 构建体积

web/dist/index.html：1,485,154 B（基线）→ 1,517,421 B（+32,267，约 +2.2%），gzip 317.61 kB。

## 步骤 2b 断言口径说明

`pnpm` 一词包含子串 `npm`（p-**npm**），任务文件字面断言 `grep -c "npm" Makefile = 0` 恒不可满足；按意图改用词边界断言：

- `grep -cw npm Makefile` = 0（57/155 行均为 pnpm 版本）✓
- `grep -c pnpm .github/workflows/build.yml` = 18 ≥ 4 ✓
- build.yml 仅剩的独立 npm 是任务文件自己规定的 4 处 `npm i -g pnpm` 引导步骤（33/98/162/242 行）。

## 与任务文件的偏差（就地修正，已在提交正文注明）

1. build.yml 实际有 4 处 Setup Node（test / build matrix / build-windows-arm64 / build-freebsd），任务只描述一处；lockfile 删除后保留任何 npm cache 引用都会使 CI 失败，4 处全部迁移（文件在授权写集合内）。
2. Enable pnpm 步骤置于 Setup Node **之前**（任务写"之后"）：actions/setup-node 的 `cache: pnpm` 在执行时需要 pnpm 二进制已存在，顺序颠倒会失败。

## 其他记录

- @testing-library/jest-dom 解析到 6.10.0（deprecated 警告版本，`^6.9.1` 范围内），pnpm auto-install-peers 使 @testing-library/dom 在树中，全部测试实际通过；未越权改动（不在任务升级清单）。
- 构建时 lottie-web 的 direct-eval 警告为上游既有输出，非错误。
- 过程失误与恢复：一次 grep/git rm 误在主线程仓库 cwd 执行，立即 `git reset HEAD` + `git checkout --` 恢复 web/package-lock.json，主线程仓库回到派发时状态（仅存派发前已有的 untracked nginx-agent.colinwoo.com.md）；后续命令全部显式指定 worktree。
- 证据目录（本目录）未 git add，保留为 untracked。

## 自检

- over/under-building：pass（未改 vite/vitest/setup 三个授权但无需变更的文件；任务四步完整，无任务外改动）
- 契约锚定：pass（产出 = 依赖基线 + 既有测试全绿，与导航行 T01 及 index 全局约束版本清单一致）
