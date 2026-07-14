<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'

import { mensagemLegivel } from '@/api/errors'
import { useTablesStore } from '@/stores/tables'
import type { Table } from '@/types/api'

const props = defineProps<{
  /** Aberto em modo edição quando vem uma mesa; em modo criação quando vem null. */
  mesa: Table | null
  aberto: boolean
}>()

const emit = defineEmits<{ fechar: []; salvo: [Table] }>()

const store = useTablesStore()

// <dialog> nativo: ESC, backdrop, trap de foco e a semântica de modal vêm do
// browser. Uma lib de modal aqui seria 12 KB para reimplementar o que a
// plataforma já faz — e faria pior.
const dialogo = ref<HTMLDialogElement>()
const campoNome = ref<HTMLInputElement>()

const nome = ref('')
const capacidade = ref<number | null>(null)
const erro = ref<string | null>(null)
const salvando = ref(false)

const editando = () => props.mesa !== null

watch(
  () => props.aberto,
  async (aberto) => {
    if (!aberto) {
      dialogo.value?.close()
      return
    }

    nome.value = props.mesa?.name ?? ''
    capacidade.value = props.mesa?.capacity ?? null
    erro.value = null

    dialogo.value?.showModal()
    // O foco vai para o primeiro campo, e não para o botão de fechar (que é onde o
    // browser o coloca sozinho). Quem abriu este modal veio para digitar.
    await nextTick()
    campoNome.value?.focus()
  },
)

async function salvar() {
  const nomeLimpo = nome.value.trim()

  // Duas checagens locais, e só duas: campo vazio e capacidade ausente. TUDO o
  // mais é do servidor — inclusive o limite superior de capacidade e a unicidade
  // do nome. Replicar as regras aqui criaria duas fontes da verdade que divergem
  // no dia em que uma delas mudar, e a UI passaria a recusar o que a API aceita.
  if (!nomeLimpo) {
    erro.value = 'Dê um nome à mesa.'
    campoNome.value?.focus()
    return
  }
  if (capacidade.value === null) {
    erro.value = 'Informe quantos lugares a mesa tem.'
    return
  }

  salvando.value = true
  erro.value = null

  try {
    const mesa = editando()
      ? await store.atualizar(props.mesa!.id, { name: nomeLimpo, capacity: capacidade.value })
      : await store.criar({ name: nomeLimpo, capacity: capacidade.value })

    emit('salvo', mesa)
    emit('fechar')
  } catch (e) {
    // O 409 ("já existe uma mesa chamada Mesa 12") mora AQUI, colado ao campo que
    // o causou, com o modal aberto e o que o staff digitou ainda na tela. É por
    // isso que o store lança em vez de guardar: um erro global teria fechado o
    // modal e perdido o texto.
    erro.value = mensagemLegivel(e)
  } finally {
    salvando.value = false
  }
}
</script>

<template>
  <dialog
    ref="dialogo"
    class="bg-ink-900 border-ink-700 text-ink-100 m-auto w-[26rem] max-w-[calc(100vw-2rem)] border p-0 shadow-2xl backdrop:bg-black/70 backdrop:backdrop-blur-[2px]"
    @close="emit('fechar')"
    @cancel="emit('fechar')"
  >
    <form method="dialog" @submit.prevent="salvar">
      <!-- A faixa de brasa no topo é o que dá ao modal a cara de comanda arrancada
           do bloco, em vez de card genérico com sombra. -->
      <div class="bg-ember-500 h-1"></div>

      <header class="border-ink-700 border-b px-6 pt-5 pb-4">
        <h2 class="font-display text-lg font-bold tracking-wide uppercase">
          {{ editando() ? 'Editar mesa' : 'Nova mesa' }}
        </h2>
        <p v-if="editando()" class="dado text-ink-500 mt-1 text-xs">
          {{ props.mesa?.id }}
        </p>
      </header>

      <div class="space-y-5 px-6 py-6">
        <div>
          <label for="nome" class="rotulo mb-2 block">Nome</label>
          <input
            id="nome"
            ref="campoNome"
            v-model="nome"
            type="text"
            autocomplete="off"
            placeholder="Mesa 12"
            class="bg-ink-950 border-ink-700 text-ink-100 placeholder:text-ink-600 focus:border-ember-500 w-full border px-3 py-2.5 text-sm transition-colors outline-none"
          />
          <p class="text-ink-500 mt-1.5 text-xs">Único no restaurante.</p>
        </div>

        <div>
          <label for="capacidade" class="rotulo mb-2 block">Lugares</label>
          <input
            id="capacidade"
            v-model.number="capacidade"
            type="number"
            min="1"
            inputmode="numeric"
            placeholder="4"
            class="dado bg-ink-950 border-ink-700 text-ink-100 placeholder:text-ink-600 focus:border-ember-500 w-24 border px-3 py-2.5 text-lg font-medium transition-colors outline-none"
          />
        </div>

        <!-- O erro do servidor, exibido como veio. A UI não interpreta a string:
             a v1 da API não tem error.code, e construir um parser sobre a
             mensagem humana seria apostar contra a própria spec. -->
        <p
          v-if="erro"
          class="border-blood-500/40 bg-blood-500/10 text-blood-300 border-l-2 px-3 py-2 text-sm"
          role="alert"
        >
          {{ erro }}
        </p>
      </div>

      <footer class="border-ink-700 bg-ink-850 flex items-center justify-end gap-2 border-t px-6 py-4">
        <button
          type="button"
          class="font-display text-ink-400 hover:text-ink-100 px-4 py-2 text-sm font-semibold tracking-wide uppercase transition-colors"
          @click="emit('fechar')"
        >
          Cancelar
        </button>
        <button
          type="submit"
          :disabled="salvando"
          class="font-display bg-ember-500 text-ink-950 hover:bg-ember-400 px-5 py-2 text-sm font-bold tracking-wide uppercase transition-colors disabled:cursor-not-allowed disabled:opacity-50"
        >
          {{ salvando ? 'Salvando…' : editando() ? 'Salvar' : 'Criar mesa' }}
        </button>
      </footer>
    </form>
  </dialog>
</template>
