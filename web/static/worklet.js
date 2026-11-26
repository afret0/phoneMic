class PCMProcessor extends AudioWorkletProcessor {
  constructor() {
    super();
    this.frameSamples = 960;
    this.out = new Int16Array(this.frameSamples);
    this.outPos = 0;
    this.sumSq = 0;
    this.baseRatio = sampleRate / 48000;
    this.trimPpm = 0;
    this.ratio = this.baseRatio;
    this.pos = 0;
    this.posQ = 65536;
    this.baseStep = Math.round(this.baseRatio * this.posQ);
    this.step = this.baseStep;
    this.hist = new Float32Array(7);
    this.taps = 8;
    this.phases = 147;
    this.knee = 0.75;
    const cutoff = Math.min(1, 48000 / sampleRate);
    this.kernel = [];
    for (let p = 0; p < this.phases; p++) {
      const frac = p / this.phases;
      const w = new Float64Array(this.taps);
      let sum = 0;
      for (let j = 0; j < this.taps; j++) {
        const t = frac + (this.taps / 2 - 1) - j;
        const x = Math.PI * cutoff * t;
        const sinc = x === 0 ? 1 : Math.sin(x) / x;
        const win = 0.42 - 0.5 * Math.cos(2 * Math.PI * j / (this.taps - 1)) + 0.08 * Math.cos(4 * Math.PI * j / (this.taps - 1));
        w[j] = sinc * win;
        sum += w[j];
      }
      for (let j = 0; j < this.taps; j++) w[j] /= sum;
      this.kernel.push(w);
    }
    this.port.onmessage = (e) => {
      if (typeof e.data.trimPpm === 'number') {
        this.trimPpm = e.data.trimPpm;
        this.ratio = this.baseRatio * (1 + this.trimPpm / 1e6);
        this.step = Math.round(this.baseStep * (1 + this.trimPpm / 1e6));
      }
    };
  }

  process(inputs) {
    const ch = inputs[0] && inputs[0][0];
    if (!ch || ch.length === 0) return true;

    if (this.ratio === 1) {
      for (let i = 0; i < ch.length; i++) this.push(ch[i]);
      this.saveHist(ch);
      this.pos = 0;
      return true;
    }

    const hist = this.hist;
    const q = this.posQ;
    const limit = (ch.length - this.taps / 2) * q;
    let pos = this.pos;
    while (pos < limit) {
      const idx = Math.floor(pos / q);
      const phase = Math.min(this.phases - 1, Math.floor((pos - idx * q) * this.phases / q));
      const k = this.kernel[phase];
      let acc = 0;
      for (let j = 0; j < this.taps; j++) {
        const at = idx - (this.taps / 2 - 1) + j;
        const s = at < 0 ? hist[7 + at] : ch[at];
        acc += s * k[j];
      }
      this.push(acc);
      pos += this.step;
    }
    this.saveHist(ch);
    this.pos = pos - ch.length * q;
    return true;
  }

  saveHist(ch) {
    for (let j = 0; j < 7; j++) {
      const at = ch.length - 7 + j;
      this.hist[j] = at < 0 ? this.hist[7 + at] : ch[at];
    }
  }

  push(v) {
    const a = Math.abs(v);
    let s = v;
    if (a > this.knee) s = Math.sign(v) * (this.knee + (1 - this.knee) * Math.tanh((a - this.knee) / (1 - this.knee)));
    s = Math.max(-1, Math.min(1, s));
    this.sumSq += s * s;
    this.out[this.outPos++] = s < 0 ? s * 0x8000 : s * 0x7fff;
    if (this.outPos === this.frameSamples) {
      const level = Math.sqrt(this.sumSq / this.frameSamples);
      const buf = this.out.buffer.slice(0);
      this.port.postMessage({ pcm: buf, level }, [buf]);
      this.outPos = 0;
      this.sumSq = 0;
    }
  }
}

registerProcessor('pcm-processor', PCMProcessor);