
        <div class="feature-card rounded-xl p-6">
          <div class="flex items-start gap-4">
            <div class="w-12 h-12 rounded-lg bg-blue-500/10 flex items-center justify-center flex-shrink-0">
              <span class="text-2xl">📁</span>
            </div>
            <div>
              <h3 class="text-xl font-semibold text-white mb-2">File transfer support</h3>
              <p class="text-slate-400 leading-relaxed">Copy files and directories to remote machines with the new <code class="text-blue-400">cp</code> command. Auto-detects files vs directories, uses tar archives for directories, and includes the <code class="text-blue-400">rcc</code> alias for quick access.</p>
            </div>
          </div>
        </div>

        <div class="feature-card rounded-xl p-6">
          <div class="flex items-start gap-4">
            <div class="w-12 h-12 rounded-lg bg-emerald-500/10 flex items-center justify-center flex-shrink-0">
              <span class="text-2xl">📊</span>
            </div>
            <div>
              <h3 class="text-xl font-semibold text-white mb-2">JSONL streaming progress</h3>
              <p class="text-slate-400 leading-relaxed">Add <code class="text-blue-400">--stream</code> to both command execution and file transfer for agent-friendly JSONL progress events. Track start, read, sent, and complete states in real time.</p>
            </div>
          </div>
        </div>

        <div class="feature-card rounded-xl p-6">
          <div class="flex items-start gap-4">
            <div class="w-12 h-12 rounded-lg bg-purple-500/10 flex items-center justify-center flex-shrink-0">
              <span class="text-2xl">🔗</span>
            </div>
            <div>
              <h3 class="text-xl font-semibold text-white mb-2">Peer pairing flow</h3>
              <p class="text-slate-400 leading-relaxed">Connect any remote machine in seconds with the new <code class="text-blue-400">pair listen</code> command. Share a one-liner curl command, and the machine automatically registers as a target.</p>
            </div>
          </div>
        </div>

        <div class="feature-card rounded-xl p-6">
          <div class="flex items-start gap-4">
            <div class="w-12 h-12 rounded-lg bg-amber-500/10 flex items-center justify-center flex-shrink-0">
              <span class="text-2xl">📚</span>
            </div>
            <div>
              <h3 class="text-xl font-semibold text-white mb-2">Documentation overhaul</h3>
              <p class="text-slate-400 leading-relaxed">Complete README rewrite with full feature coverage, new <code class="text-blue-400">docs/</code> directory with landing page and changelogs, and SuperCLI integration for easy discovery.</p>
            </div>
          </div>
        </div>

        <div class="feature-card rounded-xl p-6">
          <div class="flex items-start gap-4">
            <div class="w-12 h-12 rounded-lg bg-rose-500/10 flex items-center justify-center flex-shrink-0">
              <span class="text-2xl">🐛</span>
            </div>
            <div>
              <h3 class="text-xl font-semibold text-white mb-2">Install script improvements</h3>
              <p class="text-slate-400 leading-relaxed">Fixed binary-busy errors by downloading to <code class="text-blue-400">/tmp</code> first, stops daemon before overwriting, and handles re-installs gracefully on machines with existing daemons.</p>
            </div>
          </div>
        </div>
