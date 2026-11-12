import { DialogService } from '../../../bindings/lontar/internal/services'

export interface FileFilter {
  displayName: string
  pattern: string
}

/** Native file pickers (Go DialogService). Both resolve "" when cancelled. */
export const dialogApi = {
  async openFile(title: string, filters: FileFilter[] = []): Promise<string> {
    return (await DialogService.OpenFile(title, filters)) ?? ''
  },
  async saveFile(title: string, filename: string, filters: FileFilter[] = []): Promise<string> {
    return (await DialogService.SaveFile(title, filename, filters)) ?? ''
  },
}
