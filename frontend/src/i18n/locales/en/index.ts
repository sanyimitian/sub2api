import adminAccessKeys from './adminAccessKeys'
import modelIdentification from './modelIdentification'
import modelBenchmark from './modelBenchmark'
import landing from './landing'
import common from './common'
import dashboard from './dashboard'
import channelMonitorV2 from './channelMonitorV2'
import batchImage from './batchImage'
import admin from './admin'
import misc from './misc'
import publicTransit from './publicTransit'

export default {
  modelBenchmark,
  modelIdentification,
  adminAccessKeys,
  ...landing,
  ...common,
  ...dashboard,
  ...channelMonitorV2,
  ...batchImage,
  admin,
  ...misc,
  ...publicTransit,
}
