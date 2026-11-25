/** Draw a static background that follows the signed-out workspace size. */
export function mountLoginScene(root: HTMLElement): (() => void) | undefined {
  const canvas = root.querySelector<HTMLCanvasElement>(".login-scene canvas");
  const ink = canvas?.getContext("2d");
  if (!canvas || !ink) return;
  const drawing = ink;
  const surface = canvas;

  const paintBackground = () => {
    const { width, height } = surface.getBoundingClientRect();
    if (!width || !height) return;
    const scale = Math.min(window.devicePixelRatio || 1, 2);
    surface.width = Math.round(width * scale);
    surface.height = Math.round(height * scale);
    drawing.setTransform(scale, 0, 0, scale, 0, 0);
    const light = drawing.createLinearGradient(0, height, width, height * .15);
    light.addColorStop(0, "rgba(71,112,214,.34)");
    light.addColorStop(.31, "rgba(119,111,220,.19)");
    light.addColorStop(.5, "rgba(127,137,226,.08)");
    light.addColorStop(.75, "rgba(96,118,215,.28)");
    light.addColorStop(1, "rgba(52,119,185,.38)");
    drawing.strokeStyle = light;
    drawing.lineWidth = .85;

    // Two sets of parallel curves leave the form in a clear, quiet part of the scene.
    const wide = width >= 650;
    const lanes = wide ? 21 : 13;
    for (let i = 0; i < lanes; i += 1) {
      const t = i / (lanes - 1);
      drawing.beginPath();
      drawing.moveTo(-width * .08, height * (.59 + .21 * t));
      drawing.bezierCurveTo(width * .10, height * (.52 + .28 * t), width * .16, height * (.89 + .13 * t), width * .41, height * (.88 + .17 * t));
      drawing.bezierCurveTo(width * .58, height * (.86 + .16 * t), width * .70, height * (.63 + .21 * t), width * 1.06, height * (.45 + .23 * t));
      drawing.stroke();
    }

    function translateTrail(points: readonly (readonly [number, number])[], alpha: number) {
      drawing.strokeStyle = `rgba(105,112,194,${alpha})`;
      drawing.lineWidth = 1;
      drawing.beginPath();
      for (const [i, [x, y]] of points.entries()) {
        if (i === 0) drawing.moveTo(x * width, y * height);
        else drawing.lineTo(x * width, y * height);
      }
      drawing.stroke();
    }

    if (wide) {
      const traces = [
        [[-.01,.29],[.11,.29],[.148,.35],[.223,.35],[.257,.407]],
        [[.065,.44],[.155,.44],[.187,.49],[.259,.49]],
        [[.722,.25],[.756,.202],[.843,.202],[.875,.156],[1.01,.156]],
        [[.788,.331],[.844,.331],[.876,.285],[.955,.285],[1.01,.343]],
      ] as const;
      for (const trace of traces) translateTrail(trace, .21);
      const nodes = [
        [.11,.29,3], [.223,.35,3], [.257,.407,4], [.065,.44,4], [.187,.49,3], [.722,.25,3], [.843,.202,4],
        [.875,.156,3], [.788,.331,4], [.955,.285,3], [.134,.663,3], [.246,.812,4], [.732,.754,4], [.907,.617,3],
      ] as const;
      for (const [x, y, size] of nodes) {
        const px = x * width;
        const py = y * height;
        drawing.fillStyle = "rgba(121,125,221,.15)";
        drawing.beginPath();
        drawing.arc(px, py, size + 4, 0, Math.PI * 2);
        drawing.fill();
        drawing.fillStyle = "#fafaff";
        drawing.strokeStyle = "rgba(83,98,204,.57)";
        drawing.beginPath();
        drawing.arc(px, py, size, 0, Math.PI * 2);
        drawing.fill();
        drawing.stroke();
      }
    }
  };
  paintBackground();
  if (typeof ResizeObserver === "undefined") return;
  const observer = new ResizeObserver(paintBackground);
  observer.observe(canvas);
  return () => observer.disconnect();
}
