# v0.2.0 发布核验

- 发布代码：`ab0d0fa78f9e410d23a59eb358a4335a1d7a4623`；annotated tag：`3d3ae020ea117041b7c3a1b5b43441def86a066e`。
- `git push --atomic origin main refs/tags/v0.2.0` exit 0；远端 main/tag 解引用均匹配发布代码。
- Tag CI 34683169869：Test、七个平台构建、Release 全部 success。Main CI 34683170429：Test 和七平台 success，Release 按工作流规则 skipped。
- Release 非 draft、非 prerelease，中文主要更新说明已写入且回读与 release-notes.md 一致。
- `gh release download v0.2.0 --dir /tmp/cpa-release-v0.2.0-7YC6EJ` exit 0。
- 在下载目录运行 `shasum -a 256 -c checksums.txt` exit 0，七个 ZIP 均 OK。
- 对七个 ZIP 分别执行 `unzip -t <文件>` 均 exit 0；内容均为对应平台 v0.2.0 动态库与 LICENSE，无压缩数据错误。
- 重新计算所有八个附件 SHA-256，与 GitHub assets.digest 全部一致。原校验和和 API 回执保存在本目录。
- 下载目录已移至本任务专属废纸篓，可恢复；不把二进制包提交进源码。
- 最终状态文档提交只改 .spec-dev；不移动已发布 tag，后续核对生产代码与发布 tag 无差异。

地址：https://github.com/FlameMida/cpa-model-mapper-plus/releases/tag/v0.2.0
