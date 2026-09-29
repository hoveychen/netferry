# 语义范围组

## 问题

旧规则页把已配置规则、实时目标和最多 1000 条历史主机合并成一列。主机名只是网络端点，不能表达“这一批域名属于同一个服务，我希望它们走同一路线”。

## 模型

每个 ProfileGroup 增加 `ruleGroups`，一项包含稳定 `id`、用户可读的 `name`、`domains` 和 `route`。一个域名范围同时覆盖域名本身和所有子域名；输入 `=host.example.com` 时只覆盖该主机。范围组可以组合不同根域，例如一个服务的网页、API 和静态资源域名。范围组只表达用户确认过的归属，不根据公司名称猜测 CDN 或第三方域名。

路由只有隧道、直连、阻止三种，不按 profile 区分；旧文件里的 `default` 与 `tunnel:<profileId>` 读为隧道。ProfileGroup 另有 `finalRoute`（隧道或直连，缺省隧道），即 Clash 的 MATCH，决定没有命中任何规则的流量。

客户端不编译规则：桌面端与 TUI 把逐主机规则、有序的范围组和 `finalRoute` 原样以 `{overrides, groups, final}` 推给侧车 `/routes`，匹配只在 `netferry-relay/internal/stats` 实现一份。未配置范围组的旧文件读为 `[]`，保存后仍保留旧 `rules`。

## 客户端

规则首页先展示已命名的范围组（路由、范围数、当前命中的历史目标数），再展示按域名归并的未分类目标。展开后才看单个主机。搜索能找到范围组名称、范围域名与主机名。创建范围组时先预览会覆盖哪些已见目标，再保存。单主机规则显示来源，方便理解为什么走某条路线。

未归组站点按已见主机数量排序，首页只展开前 20 个；其余站点可通过搜索或“查看全部”进入。这个折叠仅影响展示，历史目标和逐项规则不会丢失。

`knownHosts` 是发现记录，不是规则。新访问的主机只进入未分类目标，不自动新增规则。

## 范围来源

站点归并使用 `tldts` 的公共后缀表提取可注册域名；它只能把 `a.example.co.uk` 归到 `example.co.uk`，不能知道服务归属。

服务建议来自 V2Fly 的 [domain-list-community](https://github.com/v2fly/domain-list-community)，固定提交 `bcea25493ed28c387660fe49ce1ceb242d2efca0`，MIT 许可证见 `docs/third-party/domain-list-community-LICENSE`。`scripts/build_service_catalog.py` 下载该提交，按标签展开 `include:`，把普通域名映射为域名及子域范围、`full:` 映射为精确主机；跳过无法准确映射的正则和关键词。具体服务清单排除 `@ads` 条目，“广告与跟踪”类别只从带 `@ads` 的嵌套条目及明确列出的提供方取范围。生成的 `src/data/serviceDomains.json` 随应用离线发布，版本可核查；目录只在打开规则页时加载。

规则首页优先展示**按路由用途分组**：内地有接入点、内地无接入点、GFWList 收录、已核实的服务地区限制。前两项分别来自同一固定 V2Fly 提交的 `geolocation-cn` 与 `geolocation-!cn`，只说明内地接入点状况，不表示服务商是否允许中国或香港用户。GFWList 来自固定提交 `3e23962592b28d64fdd7cc76505bc22af6ac75c8`，仅保留可转成域名范围的规则；遇到允许例外时保守地丢弃冲突范围。GFWList 是社区规则，不是实时封锁测试；其 LGPL 2.1 许可见 `docs/third-party/gfwlist-LICENSE`。

“已核实的服务地区限制”截至 2026-09-28 仅含四个精确产品主机：`=api.openai.com`（[OpenAI API 支持地区](https://developers.openai.com/api/docs/supported-countries)）、`=api.anthropic.com`（[Claude API 支持地区](https://platform.claude.com/docs/en/api/supported-regions)）、`=claude.ai`（[Claude 网页支持地区](https://support.claude.com/en/articles/8461763-where-can-i-access-claude-ai)）和 `=generativelanguage.googleapis.com`（[Gemini API 可用地区](https://ai.google.dev/gemini-api/docs/available-regions)）。这些官方列表有完整支持地区，未列中国内地和香港；客户端的预览为各主机提供对应证据链接。不能据此推断同一公司其他域名或产品的政策。服务商还可能检查账号地区，改变网络路线不保证服务可用。

这些是**独立线索**：同一目标可以同时没有内地接入点并被 GFWList 收录。客户端仅对已见目标推荐命中的范围；内地接入点建议直连，其他范围建议默认隧道，均须用户检查预览并保存才生效。已有命名范围组保持在首页最上方，品牌服务候选作为次级细分保留。不能把“海外”“被拦截”“服务商地区限制”互相等同。

客户端只用目录识别已见目标，候选范围只包含实际命中的目录条目。用户在预览后确认，才会写入 `ruleGroups` 并影响路由。目录是社区维护的归属线索，不能保证覆盖全部服务域名，也可能存在共享基础设施；因此不自动应用目录的全部条目或改变路由。未识别的目标仍按站点显示，用户可手动建组。

纯 IP 不能可靠反推服务，单独收在“IP 地址”入口；它们仍可搜索并设置精确规则。宽类别（金融、娱乐、社交等）排在具体服务之后，减少同域名归属冲突。

## 优先级

逐主机规则（精确优先，其次最具体的 `*.suffix`）> 范围组（按列表从上到下，第一个有域名命中的组生效，与域名具体程度无关）> `finalRoute`。规则页按这个顺序展示，并提供上移、下移调整范围组顺序。优先级数字仍独立存在，不随范围组变化。
