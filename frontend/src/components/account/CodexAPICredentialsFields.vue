<template>
  <div class="space-y-4">
    <div>
      <label class="input-label" for="codex-api-base-url">{{ t('admin.accounts.baseUrl') }}</label>
      <input id="codex-api-base-url" v-model="baseUrl" type="url" class="input" required />
    </div>
    <div>
      <label class="input-label" for="codex-api-key">{{ t('admin.accounts.apiKey') }}</label>
      <input id="codex-api-key" v-model="apiKey" type="password" class="input" autocomplete="new-password" :required="!editing" :placeholder="editing ? t('admin.accounts.leaveEmptyToKeep') : ''" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

defineProps<{ editing?: boolean }>()
const baseUrl = defineModel<string>('baseUrl', { required: true })
const apiKey = defineModel<string>('apiKey', { required: true })
const { t } = useI18n()
</script>
