// 极简按行 diff（LCS），用于产出物版本对比。纯函数，不依赖 DOM。
const MAX_LINES = 1500;

export function diffLines(oldText, newText) {
  const A = String(oldText == null ? "" : oldText).split("\n");
  const B = String(newText == null ? "" : newText).split("\n");
  if (A.length > MAX_LINES || B.length > MAX_LINES) {
    return [{ t: "meta", v: "内容过长（>" + MAX_LINES + " 行），仅显示版本切换" }];
  }
  const n = A.length;
  const m = B.length;
  const dp = new Array(n + 1);
  for (let i = 0; i <= n; i++) dp[i] = new Uint16Array(m + 1);
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      dp[i][j] = A[i] === B[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
    }
  }
  const out = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (A[i] === B[j]) { out.push({ t: "eq", v: A[i] }); i++; j++; }
    else if (dp[i + 1][j] >= dp[i][j + 1]) { out.push({ t: "del", v: A[i] }); i++; }
    else { out.push({ t: "add", v: B[j] }); j++; }
  }
  while (i < n) { out.push({ t: "del", v: A[i] }); i++; }
  while (j < m) { out.push({ t: "add", v: B[j] }); j++; }
  return out;
}

export function diffStat(ops) {
  let add = 0;
  let del = 0;
  for (let i = 0; i < ops.length; i++) {
    if (ops[i].t === "add") add++;
    else if (ops[i].t === "del") del++;
  }
  return { add: add, del: del };
}
