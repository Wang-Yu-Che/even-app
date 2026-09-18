import {
  waitForEvenAppBridge, CreateStartUpPageContainer, RebuildPageContainer,
  TextContainerProperty, ImageContainerProperty, TextContainerUpgrade, ImageRawDataUpdate,
  ListContainerProperty, ListItemContainerProperty,
} from '@evenrealities/even_hub_sdk'

type Page = {
  list?: { items: string[]; itemWidth: number; selectBorder: boolean }
  text: string
  style: { X: number; Y: number; Width: number; Height: number; BorderWidth: number; BorderColor: number; BorderRadius: number; PaddingLength: number; ListItemWidth: number }
  icon: { ID: number; Name: string; X: number; Y: number; Width: number; Height: number; BMP: string } | null
}

async function main() {
  const bridge = await waitForEvenAppBridge()
  let created = false
  let previousLayout = '', previousText = '', previousBMP = ''
  async function render() {
    try {
      const response = await fetch('/frame', { cache: 'no-store' })
      if (!response.ok) throw new Error(`读取页面失败：${response.status}`)
      const page: Page = await response.json()
      const { style, icon } = page
      const text = new TextContainerProperty({
        containerID: 1, containerName: 'agent-stream', isEventCapture: 1,
        xPosition: style.X, yPosition: style.Y, width: style.Width, height: style.Height,
        borderWidth: style.BorderWidth, borderColor: style.BorderColor,
        borderRadius: style.BorderRadius, paddingLength: style.PaddingLength,
        content: page.text || ' ',
      })
      const image = icon ? new ImageContainerProperty({
        containerID: icon.ID, containerName: icon.Name,
        xPosition: icon.X, yPosition: icon.Y, width: icon.Width, height: icon.Height,
      }) : null
      const layout = JSON.stringify([style, image, page.list])
      const containers = page.list ? {
        containerTotalNum: 1,
        listObject: [new ListContainerProperty({
          containerID: 1, containerName: 'agent-stream', isEventCapture: 1,
          xPosition: style.X, yPosition: style.Y, width: style.Width, height: style.Height,
          borderWidth: style.BorderWidth, borderColor: style.BorderColor,
          borderRadius: style.BorderRadius, paddingLength: style.PaddingLength,
          itemContainer: new ListItemContainerProperty({
            itemCount: page.list.items.length, itemWidth: page.list.itemWidth,
            isItemSelectBorderEn: page.list.selectBorder ? 1 : 0, itemName: page.list.items,
          }),
        })],
      } : { containerTotalNum: image ? 2 : 1, textObject: [text], imageObject: image ? [image] : [] }
      if (!created) {
        const result = await bridge.createStartUpPageContainer(new CreateStartUpPageContainer(containers))
        if (result !== 0) throw new Error(`创建页面失败：${result}`)
        created = true
        previousLayout = layout
        previousText = page.text
      } else if (layout !== previousLayout) {
        if (!await bridge.rebuildPageContainer(new RebuildPageContainer(containers))) throw new Error('重建页面失败')
        previousLayout = layout
        previousText = page.text
        previousBMP = ''
      } else if (page.text !== previousText) {
        if (!await bridge.textContainerUpgrade(new TextContainerUpgrade({
          containerID: 1, containerName: 'agent-stream', content: page.text || ' ', contentOffset: 0, contentLength: 0,
        }))) throw new Error('更新文本失败')
        previousText = page.text
      }
      if (icon && icon.BMP !== previousBMP) {
        const result = await bridge.updateImageRawData(new ImageRawDataUpdate({
          containerID: icon.ID, containerName: icon.Name,
          imageData: Uint8Array.from(atob(icon.BMP), char => char.charCodeAt(0)),
        }))
        if (result !== 'success') throw new Error(`更新图标失败：${result}`)
        previousBMP = icon.BMP
      }
      document.getElementById('status')!.textContent = '页面已同步'
    } catch (error) {
      console.error(error)
      document.getElementById('status')!.textContent = String(error)
    }
    window.setTimeout(render, 250)
  }
  await render()
}

void main().catch(error => {
  console.error(error)
  document.getElementById('status')!.textContent = String(error)
})
