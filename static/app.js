(() => {
  const numberFormat = new Intl.NumberFormat(undefined, { maximumFractionDigits: 0 });

  function readPoints(root) {
    const data = root.querySelector("#telemetry-data");
    if (!data) return [];
    try {
      const points = JSON.parse(data.textContent || "[]");
      return Array.isArray(points) ? points : [];
    } catch {
      return [];
    }
  }

  function drawChart(root) {
    const canvas = root.querySelector("#moisture-chart");
    const empty = root.querySelector("#chart-empty");
    if (!canvas || !empty) return;
    const points = readPoints(root).filter((point) => Number.isFinite(Number(point.value)) && point.timestamp);
    const ctx = canvas.getContext("2d");
    const width = canvas.clientWidth;
    const height = canvas.clientHeight;
    const ratio = Math.max(1, window.devicePixelRatio || 1);
    canvas.width = Math.floor(width * ratio);
    canvas.height = Math.floor(height * ratio);
    ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
    ctx.clearRect(0, 0, width, height);

    if (!points.length) {
      empty.hidden = false;
      return;
    }
    empty.hidden = true;

    const compact = width < 520;
    const pad = { top: 18, right: 15, bottom: compact ? 30 : 36, left: compact ? 42 : 52 };
    const plotWidth = Math.max(1, width - pad.left - pad.right);
    const plotHeight = Math.max(1, height - pad.top - pad.bottom);
    const values = points.map((point) => Number(point.value));
    let min = Math.min(...values);
    let max = Math.max(...values);
    const range = Math.max(30, max - min);
    min = Math.max(0, min - range * 0.16);
    max += range * 0.16;
    if (max === min) max = min + 1;

    ctx.font = "11px Consolas, monospace";
    ctx.textBaseline = "middle";
    ctx.lineWidth = 1;
    ctx.strokeStyle = "rgba(33, 49, 40, 0.10)";
    ctx.fillStyle = "#738075";
    for (let row = 0; row <= 3; row += 1) {
      const y = pad.top + (plotHeight / 3) * row;
      const label = max - ((max - min) / 3) * row;
      ctx.beginPath();
      ctx.moveTo(pad.left, y);
      ctx.lineTo(width - pad.right, y);
      ctx.stroke();
      ctx.textAlign = "right";
      ctx.fillText(numberFormat.format(label), pad.left - 9, y);
    }

    const firstTime = new Date(points[0].timestamp).getTime();
    const lastTime = new Date(points[points.length - 1].timestamp).getTime();
    const span = Math.max(1, lastTime - firstTime);
    const coords = points.map((point, index) => {
      const time = new Date(point.timestamp).getTime();
      const x = points.length === 1 ? pad.left + plotWidth / 2 : pad.left + ((time - firstTime) / span) * plotWidth;
      const y = pad.top + ((max - Number(point.value)) / (max - min)) * plotHeight;
      return { x, y, time, value: Number(point.value), index };
    });

    const gradient = ctx.createLinearGradient(0, pad.top, 0, pad.top + plotHeight);
    gradient.addColorStop(0, "rgba(111, 139, 75, 0.24)");
    gradient.addColorStop(1, "rgba(111, 139, 75, 0.015)");
    ctx.beginPath();
    ctx.moveTo(coords[0].x, pad.top + plotHeight);
    coords.forEach((point) => ctx.lineTo(point.x, point.y));
    ctx.lineTo(coords[coords.length - 1].x, pad.top + plotHeight);
    ctx.closePath();
    ctx.fillStyle = gradient;
    ctx.fill();

    ctx.beginPath();
    coords.forEach((point, index) => {
      if (index === 0) ctx.moveTo(point.x, point.y);
      else ctx.lineTo(point.x, point.y);
    });
    ctx.strokeStyle = "#6f8b4b";
    ctx.lineWidth = 2.5;
    ctx.lineJoin = "round";
    ctx.lineCap = "round";
    ctx.stroke();

    coords.forEach((point, index) => {
      if (coords.length > 36 && index % Math.ceil(coords.length / 18) !== 0 && index !== coords.length - 1) return;
      ctx.beginPath();
      ctx.arc(point.x, point.y, index === coords.length - 1 ? 4 : 2.5, 0, Math.PI * 2);
      ctx.fillStyle = index === coords.length - 1 ? "#213128" : "#f3f3ed";
      ctx.fill();
      ctx.strokeStyle = "#6f8b4b";
      ctx.lineWidth = 1.5;
      ctx.stroke();
    });

    const tickCount = compact ? 3 : 5;
    ctx.fillStyle = "#738075";
    ctx.textAlign = "center";
    ctx.textBaseline = "top";
    for (let i = 0; i < tickCount; i += 1) {
      const ratioAt = tickCount === 1 ? 0 : i / (tickCount - 1);
      const date = new Date(firstTime + span * ratioAt);
      const label = new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(date);
      const x = pad.left + plotWidth * ratioAt;
      ctx.fillText(label, x, pad.top + plotHeight + 13);
    }
  }

  function initialize() {
    const root = document.querySelector("#dashboard");
    if (!root) return;
    drawChart(root);
  }

  let resizeFrame = 0;
  window.addEventListener("resize", () => {
    cancelAnimationFrame(resizeFrame);
    resizeFrame = requestAnimationFrame(initialize);
  });

  initialize();
  window.setInterval(async () => {
    const shell = document.querySelector("#dashboard-shell");
    if (!shell || document.hidden) return;
    try {
      const response = await fetch("/dashboard", { headers: { Accept: "text/html" }, cache: "no-store" });
      if (!response.ok) return;
      shell.innerHTML = await response.text();
      initialize();
    } catch {
      // Keep the last successful view on screen during a temporary outage.
    }
  }, 60_000);
})();
