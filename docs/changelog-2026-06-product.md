
        <div class="feature-card rounded-xl p-6">
          <div class="flex items-start gap-4">
            <div class="w-12 h-12 rounded-lg bg-blue-500/10 flex items-center justify-center flex-shrink-0">
              <span class="text-2xl">🔗</span>
            </div>
            <div>
              <h3 class="text-xl font-semibold text-white mb-2">One-command pairing</h3>
              <p class="text-slate-400 leading-relaxed">Connect any remote machine in seconds — no SSH keys, no open ports. Run <code class="text-blue-400">pair listen</code>, share one curl one-liner with your peer, and the machine appears as a target automatically.</p>
            </div>
          </div>
        </div>

        <div class="feature-card rounded-xl p-6">
          <div class="flex items-start gap-4">
            <div class="w-12 h-12 rounded-lg bg-emerald-500/10 flex items-center justify-center flex-shrink-0">
              <span class="text-2xl">⚡</span>
            </div>
            <div>
              <h3 class="text-xl font-semibold text-white mb-2">Real-time streaming output</h3>
              <p class="text-slate-400 leading-relaxed">Add <code class="text-blue-400">--stream</code> to any command and watch stdout/stderr appear line-by-line in real time. Perfect for following build logs, tailing files, or monitoring long-running processes.</p>
            </div>
          </div>
        </div>

        <div class="feature-card rounded-xl p-6">
          <div class="flex items-start gap-4">
            <div class="w-12 h-12 rounded-lg bg-purple-500/10 flex items-center justify-center flex-shrink-0">
              <span class="text-2xl">📦</span>
            </div>
            <div>
              <h3 class="text-xl font-semibold text-white mb-2">Zero-friction install on any machine</h3>
              <p class="text-slate-400 leading-relaxed">The install script detects arch, stops any running daemon, downloads to <code class="text-blue-400">/tmp</code> to avoid file-busy errors, then sets up a persistent systemd service — or falls back to nohup automatically.</p>
            </div>
          </div>
        </div>

        <div class="feature-card rounded-xl p-6">
          <div class="flex items-start gap-4">
            <div class="w-12 h-12 rounded-lg bg-amber-500/10 flex items-center justify-center flex-shrink-0">
              <span class="text-2xl">🎯</span>
            </div>
            <div>
              <h3 class="text-xl font-semibold text-white mb-2">Named aliases with relay routing</h3>
              <p class="text-slate-400 leading-relaxed">Give targets friendly names with <code class="text-blue-400">--name</code>. Aliases are transparently resolved to the relay-registered hostname so commands always reach the right machine.</p>
            </div>
          </div>
        </div>

        <div class="feature-card rounded-xl p-6">
          <div class="flex items-start gap-4">
            <div class="w-12 h-12 rounded-lg bg-red-500/10 flex items-center justify-center flex-shrink-0">
              <span class="text-2xl">🐛</span>
            </div>
            <div>
              <h3 class="text-xl font-semibold text-white mb-2">Reliability fixes</h3>
              <p class="text-slate-400 leading-relaxed">Fixed binary-busy install failures, cleaned up unused imports, and hardened the install script to handle re-installs gracefully on machines with an existing daemon running.</p>
            </div>
          </div>
        </div>
