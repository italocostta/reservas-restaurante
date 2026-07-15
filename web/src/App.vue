<script setup lang="ts">
import { RouterLink, RouterView, useRoute } from 'vue-router'
import { computed } from 'vue'

const route = useRoute()
const titulo = computed(() => (route.meta.titulo as string) ?? '')

// A Agenda vem primeiro porque é a tela do serviço — a que se abre cinquenta vezes
// por noite. O Salão é manutenção: mexe-se nele uma vez por semestre.
const navegacao = [
  { para: '/reservas', rotulo: 'Agenda' },
  { para: '/mesas', rotulo: 'Salão' },
  { para: '/restaurante', rotulo: 'Restaurante' },
]
</script>

<template>
  <div class="min-h-screen">
    <!-- A barra é uma RÉGUA, não um header: fina, densa, colada no topo. Um
         header alto e arejado rouba a linha de cima da tela — que é justamente a
         que o staff olha primeiro. -->
    <header
      class="border-ink-700 bg-ink-900/80 sticky top-0 z-20 border-b backdrop-blur-sm"
    >
      <div class="mx-auto flex h-14 max-w-6xl items-center gap-8 px-6">
        <!-- A marca é uma brasa: o quadrado laranja é o único elemento
             puramente decorativo da interface inteira, e ele existe para dar um
             ponto de ancoragem ao olho no canto. -->
        <div class="flex items-center gap-2.5">
          <span class="bg-ember-500 block h-3 w-3" aria-hidden="true"></span>
          <span
            class="font-display text-ink-100 text-sm font-bold tracking-[0.2em] uppercase"
          >
            Passe
          </span>
        </div>

        <nav class="flex items-center gap-1">
          <RouterLink
            v-for="item in navegacao"
            :key="item.para"
            :to="item.para"
            class="font-display text-ink-400 hover:text-ink-100 relative px-3 py-1.5 text-sm font-semibold tracking-wide uppercase transition-colors"
            active-class="!text-ink-100"
          >
            {{ item.rotulo }}
            <!-- O sublinhado de ativo é uma barra sólida de brasa, não uma pílula
                 arredondada com fundo. Pílula é o vocabulário de todo dashboard
                 genérico; a barra é o de um letreiro. -->
            <span
              v-if="$route.path.startsWith(item.para)"
              class="bg-ember-500 absolute inset-x-3 -bottom-px h-0.5"
            ></span>
          </RouterLink>
        </nav>

        <span v-if="titulo" class="rotulo ml-auto">{{ titulo }}</span>
      </div>
    </header>

    <main class="mx-auto max-w-6xl px-6 py-10">
      <RouterView />
    </main>
  </div>
</template>
