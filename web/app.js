const $ = (selector) => document.querySelector(selector);
const diskList = $("#disk-list");
const networkList = $("#network-list");
const containerList = $("#container-list");
const mobileList = $("#mobile-container-list");

function bytes(value) {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  const amount = value / 1024 ** index;
  return `${amount.toFixed(index > 0 && amount < 10 ? 1 : 0)} ${units[index]}`;
}

function percent(value) {
  return `${(Number(value) || 0).toFixed(1)}%`;
}

function safeText(value) {
  return String(value ?? "").replace(/[&<>"']/g, (char) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  })[char]);
}

function progressClass(value) {
  if (value >= 90) return "critical";
  if (value >= 75) return "high";
  if (value >= 55) return "medium";
  return "";
}

function setRing(name, value) {
  const amount = Number(value) || 0;
  $(`#${name}-value`).innerHTML = `${amount.toFixed(1)}<small>%</small>`;
  $(`#${name}-ring-value`).textContent = `${Math.round(amount)}%`;
  $(`#${name}-ring`).style.setProperty("--progress", Math.min(amount, 100));
}

function renderDisks(disks) {
  $("#disk-count").textContent = `${disks.length} 个卷`;
  if (!disks.length) {
    diskList.innerHTML = '<div class="empty-state"><span class="empty-icon">◌</span><span>未发现可读取的存储卷</span></div>';
    return;
  }
  diskList.innerHTML = disks.map((disk) => {
    const usage = Number(disk.usedPercent) || 0;
    return `<article class="disk-card">
      <div class="disk-top">
        <div class="disk-name"><span class="disk-icon">▤</span><div><div class="disk-label">${safeText(disk.name)} <span style="color:var(--faint)">· ${safeText(disk.fileSystem)}</span></div><div class="disk-mount">${safeText(disk.mount)}</div></div></div>
        <span class="disk-percent">${percent(usage)}</span>
      </div>
      <div class="progress-track"><div class="progress-fill ${progressClass(usage)}" style="width:${Math.min(usage, 100)}%"></div></div>
      <div class="disk-bottom"><span>已用 <strong>${bytes(disk.usedBytes)}</strong></span><span>共 <strong>${bytes(disk.totalBytes)}</strong></span></div>
    </article>`;
  }).join("");
}

function renderNetwork(interfaces) {
  $("#network-count").textContent = `${interfaces.length} 个网卡 · 约 1 秒采样`;
  if (!interfaces.length) {
    networkList.innerHTML = '<div class="empty-state"><span class="empty-icon">◌</span><span>未发现可读取的网络接口</span></div>';
    return;
  }
  networkList.innerHTML = interfaces.map((network) => `
    <article class="network-card">
      <div class="network-card-head">
        <span class="network-icon" aria-hidden="true">⌁</span>
        <div><div class="network-name">${safeText(network.name)}</div><div class="network-label">NAS 网卡流量 · 约 1 秒采样</div></div>
      </div>
      <div class="network-rate">
        <div><span class="network-rate-label"><span class="network-receive">↓</span> 接收</span><strong class="network-rate-value">${bytes(Number(network.receiveBytesPerSecond) || 0)}/s</strong></div>
        <div><span class="network-rate-label"><span class="network-send">↑</span> 发送</span><strong class="network-rate-value">${bytes(Number(network.sendBytesPerSecond) || 0)}/s</strong></div>
      </div>
    </article>`).join("");
}

function statusMarkup(status) {
  const stopped = /exited|created|dead|paused/i.test(status);
  const error = status === "资源读取失败";
  return `<span class="status ${error ? "error" : stopped ? "stopped" : ""}">${safeText(status || "未知")}</span>`;
}

function containerMarkup(container) {
  const cpu = Number(container.cpuPercent) || 0;
  const memory = Number(container.memoryPercent) || 0;
  const memoryLabel = container.memoryLimit
    ? `${bytes(container.memoryBytes)} / ${bytes(container.memoryLimit)}`
    : bytes(container.memoryBytes);
  return {
    row: `<tr>
      <td><div class="name-cell"><span class="docker-mark">▦</span><div><div class="container-name">${safeText(container.name)}</div><div class="container-image">${safeText(container.image)}</div></div></div></td>
      <td>${statusMarkup(container.status)}</td>
      <td><div class="resource-cell"><span>${percent(cpu)}</span><div class="resource-track"><span class="cpu-fill" style="width:${Math.min(cpu, 100)}%"></span></div></div></td>
      <td><div class="resource-cell"><span>${percent(memory)}</span><div class="resource-track"><span style="width:${Math.min(memory, 100)}%"></span></div></div></td>
      <td class="io-cell">${bytes(container.networkBytes)}</td><td class="io-cell">${bytes(container.blockBytes)}</td>
    </tr>`,
    mobile: `<article class="mobile-container-card">
      <div class="mobile-container-head"><div class="name-cell"><span class="docker-mark">▦</span><div style="min-width:0"><div class="container-name">${safeText(container.name)}</div><div class="container-image">${safeText(container.image)}</div></div></div>${statusMarkup(container.status)}</div>
      <div class="mobile-stats">
        <div><div class="mobile-stat-label">CPU</div><div class="mobile-stat-value">${percent(cpu)}</div><div class="resource-track mobile-resource"><span class="cpu-fill" style="width:${Math.min(cpu, 100)}%"></span></div></div>
        <div><div class="mobile-stat-label">内存</div><div class="mobile-stat-value">${percent(memory)} · ${memoryLabel}</div><div class="resource-track mobile-resource"><span style="width:${Math.min(memory, 100)}%"></span></div></div>
        <div><div class="mobile-stat-label">网络 I/O</div><div class="mobile-stat-value">${bytes(container.networkBytes)}</div></div>
        <div><div class="mobile-stat-label">磁盘 I/O</div><div class="mobile-stat-value">${bytes(container.blockBytes)}</div></div>
      </div>
    </article>`,
  };
}

function renderContainers(containers) {
  const running = containers.filter((container) => /^Up\b/i.test(container.status)).length;
  $("#container-count").innerHTML = `${containers.length}<small> 个</small>`;
  $("#running-count").textContent = `${running} / ${containers.length}`;
  $("#container-summary").textContent = containers.length
    ? `${running} 个容器正在运行`
    : "当前没有运行中的容器";
  if (!containers.length) {
    containerList.innerHTML = '<tr><td colspan="6"><div class="empty-state"><span class="empty-icon">◌</span><span>暂无运行中的容器</span></div></td></tr>';
    mobileList.innerHTML = '<div class="empty-state"><span class="empty-icon">◌</span><span>暂无运行中的容器</span></div>';
    return;
  }
  const rows = containers.map(containerMarkup);
  containerList.innerHTML = rows.map((item) => item.row).join("");
  mobileList.innerHTML = rows.map((item) => item.mobile).join("");
}

function render(snapshot) {
  $("#hostname").textContent = snapshot.hostname || "Synology NAS";
  setRing("cpu", snapshot.cpu?.usedPercent);
  $("#cpu-status").textContent = Number(snapshot.cpu?.usedPercent) > 85 ? "负载较高" : "运行正常";

  const memory = snapshot.memory || {};
  $("#memory-value").innerHTML = `${(Number(memory.usedPercent) || 0).toFixed(1)}<small>%</small>`;
  $("#memory-ring-value").textContent = `${Math.round(Number(memory.usedPercent) || 0)}%`;
  $("#memory-ring").style.setProperty("--progress", Math.min(Number(memory.usedPercent) || 0, 100));
  $("#memory-detail").textContent = `${bytes(memory.usedBytes)} / ${bytes(memory.totalBytes)}`;
  $("#memory-status").textContent = `${bytes(Math.max(0, (memory.totalBytes || 0) - (memory.usedBytes || 0)))} 可用`;

  renderDisks(snapshot.disks || []);
  renderNetwork(snapshot.network || []);
  renderContainers(snapshot.containers || []);
  $("#last-updated").textContent = new Intl.DateTimeFormat("zh-CN", {
    hour: "2-digit", minute: "2-digit", second: "2-digit",
  }).format(new Date(snapshot.timestamp));
}

const connection = $("#connection");
const connect = () => {
  const source = new EventSource("/api/events");
  source.addEventListener("metrics", (event) => {
    try {
      render(JSON.parse(event.data));
      connection.className = "connection-pill connected";
      connection.lastElementChild.textContent = "实时连接";
    } catch (error) {
      console.error("无法解析监控数据", error);
    }
  });
  source.onopen = () => {
    connection.className = "connection-pill connected";
    connection.lastElementChild.textContent = "实时连接";
  };
  source.onerror = () => {
    connection.className = "connection-pill disconnected";
    connection.lastElementChild.textContent = "重新连接中";
  };
};

connect();
